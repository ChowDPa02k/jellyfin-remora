//go:build linux

package platform

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"golang.org/x/sys/unix"
)

func TestParseLinuxMountInfoEscapesAndCIFS(t *testing.T) {
	raw := "36 25 0:32 / /mnt/name\\040with\\040spaces rw,nosuid shared:1 - cifs //nas/share rw,vers=3.1.1\n" +
		"37 25 8:1 / /srv/data ro,relatime - ext4 /dev/sda1 ro,errors=remount-ro\n"
	mounts, err := parseLinuxMountInfo([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 2 || mounts[0].Target != "/mnt/name with spaces" || mounts[0].FSType != "cifs" || mounts[0].Source != "//nas/share" {
		t.Fatalf("unexpected mounts: %#v", mounts)
	}
	if !strings.Contains(mounts[1].Options, "errors=remount-ro") {
		t.Fatalf("superblock options missing: %#v", mounts[1])
	}
}

func TestParseLinuxProcStatHandlesParentheses(t *testing.T) {
	fields := []string{"S", "10", "11", "0", "0", "0", "0", "0", "0", "0", "0", "12", "3", "0", "0", "0", "0", "0", "0", "456", "0", "7"}
	process, err := parseLinuxProcStat([]byte("42 (name with ) parenthesis) " + strings.Join(fields, " ")))
	if err != nil {
		t.Fatal(err)
	}
	if process.parent != 10 || process.group != 11 || process.state != "S" || process.userTicks != 15 || process.startTick != 456 || process.rssPages != 7 {
		t.Fatalf("unexpected process: %#v", process)
	}
}

func TestLinuxResolvePhysicalRejectsCharacterDevice(t *testing.T) {
	backend := newBackend().(*linuxBackend)
	_, err := backend.ResolvePhysical(context.Background(), config.DiskConfig{Device: "/dev/null"})
	if err == nil || !strings.Contains(err.Error(), "not a block device") {
		t.Fatalf("ResolvePhysical(/dev/null) error = %v", err)
	}
}

func TestLinuxMountOptionsExplicitValuesOverrideDefaults(t *testing.T) {
	options := linuxMountOptions(config.DiskConfig{
		Options:    "hard,timeo=80,retrans=4,rw",
		Permission: "r",
	}, []string{"soft", "timeo=50", "retrans=2"})
	if len(options) != 2 || options[0] != "-o" {
		t.Fatalf("mount options = %#v", options)
	}
	if got := options[1]; got != "hard,timeo=80,retrans=4,rw" {
		t.Fatalf("merged mount options = %q", got)
	}
}

func TestLinuxMountTimeoutDoesNotStackBlockedHelper(t *testing.T) {
	oldKill := killLinuxMountProcessGroup
	killLinuxMountProcessGroup = func(int) error { return nil }
	t.Cleanup(func() { killLinuxMountProcessGroup = oldKill })

	backend := newBackend().(*linuxBackend)
	pidFile := filepath.Join(t.TempDir(), "mount-helper.pid")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	args := []string{"-c", `echo $$ > "$1"; while :; do sleep 5; done`, "sh", pidFile, "/mnt/test"}
	err := backend.runMount(ctx, "/mnt/test", "/bin/sh", args, nil)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("mount timeout error = %v", err)
	}
	if time.Since(started) > 500*time.Millisecond {
		t.Fatalf("mount timeout took %s", time.Since(started))
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = unix.Kill(-pid, unix.SIGKILL)
	})
	restartedBackend := newBackend().(*linuxBackend)
	started = time.Now()
	err = restartedBackend.runMount(context.Background(), "/mnt/test", "/bin/sh", args, nil)
	if err == nil || !strings.Contains(err.Error(), "remains blocked") {
		t.Fatalf("post-restart mount error = %v", err)
	}
	if time.Since(started) > 200*time.Millisecond {
		t.Fatalf("post-restart blocked mount detection took %s", time.Since(started))
	}
	started = time.Now()
	err = backend.runMount(context.Background(), "/mnt/test", "/bin/true", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "remains blocked") {
		t.Fatalf("second mount error = %v", err)
	}
	if time.Since(started) > 200*time.Millisecond {
		t.Fatalf("blocked mount suppression took %s", time.Since(started))
	}
}

func TestLinuxSMBFileCredentialUsesPinnedDescriptor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "share.credential")
	contents := []byte("username=media\npassword=secret\n")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	option, files, cleanup, err := linuxSMBCredential(context.Background(), "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if option != "credentials=/proc/self/fd/3" || len(files) != 1 {
		t.Fatalf("credential transport = %q, files=%d", option, len(files))
	}
	got, err := io.ReadAll(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(contents) {
		t.Fatalf("credential contents = %q", got)
	}
}

func TestLinuxSMBFileCredentialRejectsSymlinkAndBroadMode(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "share.credential")
	if err := os.WriteFile(path, []byte("username=a\npassword=b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := linuxSMBCredential(context.Background(), path); err == nil || !strings.Contains(err.Error(), "inaccessible to group/other") {
		t.Fatalf("broad credential mode error = %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "linked.credential")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := linuxSMBCredential(context.Background(), link); err == nil {
		t.Fatal("symlink credential was accepted")
	}
}

func TestLinuxSMBLibsecretCredentialUsesAnonymousDescriptor(t *testing.T) {
	oldLookup := lookupLinuxSecret
	lookupLinuxSecret = func(_ context.Context, key string) ([]byte, error) {
		if key != "media" {
			t.Fatalf("lookup key = %q", key)
		}
		return []byte("username=media\npassword=secret\n"), nil
	}
	t.Cleanup(func() { lookupLinuxSecret = oldLookup })
	option, files, cleanup, err := linuxSMBCredential(context.Background(), "libsecret:media")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if option != "credentials=/proc/self/fd/3" || len(files) != 1 {
		t.Fatalf("credential transport = %q, files=%d", option, len(files))
	}
	contents, err := io.ReadAll(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "username=media\npassword=secret\n" {
		t.Fatalf("credential contents = %q", contents)
	}
}

func TestLinuxProcessInfoReportsListenerAndIdentity(t *testing.T) {
	backend := newBackend().(*linuxBackend)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	wantPort := listener.Addr().(*net.TCPAddr).Port

	info, err := backend.ProcessInfo(context.Background(), os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if info.PID != os.Getpid() || info.PGID <= 0 || info.State == "" || info.MemoryBytes == 0 || info.StartedAt.IsZero() {
		t.Fatalf("incomplete process info: %#v", info)
	}
	found := false
	for _, port := range info.Ports {
		if port == wantPort {
			found = true
		}
	}
	if !found {
		t.Fatalf("listener %d not found in %v", wantPort, info.Ports)
	}
}

func TestLinuxFindProcessesUsesExecutableAndExactArguments(t *testing.T) {
	backend := newBackend().(*linuxBackend)
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep is unavailable")
	}
	marker := "47"
	cmd := exec.Command(sleep, marker)
	if err := backend.ConfigureProcess(cmd, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backend.SignalGroup(cmd.Process.Pid, true); _ = cmd.Wait() }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		matches, findErr := backend.FindProcesses(context.Background(), sleep, []string{marker})
		if findErr != nil {
			t.Fatal(findErr)
		}
		for _, match := range matches {
			if match.PID == cmd.Process.Pid {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d not found in %#v", cmd.Process.Pid, matches)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLinuxSignalGroupCleansDescendants(t *testing.T) {
	backend := newBackend().(*linuxBackend)
	if err := backend.PrepareSupervisor(); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	cmd := exec.Command("/bin/sh", "-c", "sleep 60 & echo $! > \"$1\"; wait", "sh", pidFile)
	if err := backend.ConfigureProcess(cmd, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backend.SignalGroup(cmd.Process.Pid, true); _ = cmd.Wait() }()
	var child int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(pidFile)
		if readErr == nil {
			child, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			if child > 0 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if child == 0 {
		t.Fatal("child PID was not recorded")
	}
	t.Cleanup(func() { _ = signalPIDFD(child, unix.SIGKILL) })
	if _, err := backend.ProcessInfo(context.Background(), cmd.Process.Pid); err != nil {
		t.Fatalf("cache managed descendants: %v", err)
	}
	if err := backend.SignalGroup(cmd.Process.Pid, true); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	// The normal manager polling path observes the root exit and asks the Linux
	// backend to reap descendants adopted through PR_SET_CHILD_SUBREAPER.
	_, _ = backend.ProcessInfo(context.Background(), cmd.Process.Pid)
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(child))); os.IsNotExist(err) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("descendant %d survived process-group cleanup", child)
}

func TestSignalEscapedLinuxDescendantsContinuesAfterError(t *testing.T) {
	var signaled []int
	injected := errors.New("permission denied")
	errs := signalEscapedLinuxDescendants(
		[]int{41, 42}, 40, 1, 40, unix.SIGKILL,
		func(int) (int, error) { return 99, nil },
		func(pid int, _ unix.Signal) error {
			signaled = append(signaled, pid)
			if pid == 41 {
				return injected
			}
			return nil
		},
	)
	if !reflect.DeepEqual(signaled, []int{41, 42}) {
		t.Fatalf("signaled descendants = %v, want both descendants", signaled)
	}
	if len(errs) != 1 || !errors.Is(errs[0], injected) {
		t.Fatalf("errors = %v, want injected error", errs)
	}
}

func TestSignalEscapedLinuxDescendantsDoesNotSkipAfterRootExit(t *testing.T) {
	var signaled []int
	errs := signalEscapedLinuxDescendants(
		[]int{41, 42}, 40, 1, -1, unix.SIGKILL,
		func(int) (int, error) { return 40, nil },
		func(pid int, _ unix.Signal) error {
			signaled = append(signaled, pid)
			return nil
		},
	)
	if len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
	if !reflect.DeepEqual(signaled, []int{41, 42}) {
		t.Fatalf("signaled descendants = %v, want both snapshotted descendants", signaled)
	}
}

func TestLinuxCgroupV2AddsEscapedFFmpegToAccounting(t *testing.T) {
	root := t.TempDir()
	procRoot := filepath.Join(root, "proc")
	cgroupRoot := filepath.Join(root, "cgroup")
	if err := os.MkdirAll(filepath.Join(procRoot, "42"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procRoot, "42", "cgroup"), []byte("0::/system.slice/jellyfin-remora.service\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(cgroupRoot, "system.slice", "jellyfin-remora.service")
	if err := os.MkdirAll(filepath.Join(unit, "transcode.scope"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unit, "cgroup.procs"), []byte("42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unit, "transcode.scope", "cgroup.procs"), []byte("99\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &linuxBackend{procRoot: procRoot, cgroupRoot: cgroupRoot, clockHz: 100}
	processes := map[int]linuxProcess{
		42: {pid: 42, parent: 1, command: "/usr/lib/jellyfin/jellyfin"},
		99: {pid: 99, parent: 1, command: "/usr/lib/jellyfin-ffmpeg/ffmpeg"},
	}
	if count := backend.countFFmpeg(processes, 42); count != 1 {
		t.Fatalf("cgroup ffmpeg count = %d", count)
	}
}

func TestLinuxOrphanSupplementOwnership(t *testing.T) {
	const root = 42
	const supervisor = 10
	tests := []struct {
		name     string
		process  linuxProcess
		inCgroup bool
		want     bool
	}{
		{
			name:    "same process group without cgroup",
			process: linuxProcess{pid: 43, parent: supervisor, group: root, command: "/usr/bin/worker"},
			want:    true,
		},
		{
			name:     "daemonized ffmpeg in service cgroup",
			process:  linuxProcess{pid: 44, parent: supervisor, group: 44, command: "/usr/lib/jellyfin-ffmpeg/ffmpeg"},
			inCgroup: true,
			want:     true,
		},
		{
			name: "concurrent internal storage probe",
			process: linuxProcess{pid: 45, parent: supervisor, group: 45, command: "/usr/bin/jellyfin-remora",
				args: []string{"/usr/bin/jellyfin-remora", "internal-probe", "--path", "/srv/jellyfin"}},
			inCgroup: true,
			want:     false,
		},
		{
			name:    "unrelated non-cgroup process",
			process: linuxProcess{pid: 46, parent: 1, group: 46, command: "/usr/bin/sleep"},
			want:    false,
		},
		{
			name:     "root process itself",
			process:  linuxProcess{pid: root, parent: supervisor, group: root, command: "/usr/lib/jellyfin/jellyfin"},
			inCgroup: true,
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldSupplementLinuxOrphan(root, supervisor, tt.process, tt.inCgroup); got != tt.want {
				t.Fatalf("shouldSupplementLinuxOrphan() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLinuxPrepareSupervisorAdoptsAndReapsOrphan(t *testing.T) {
	backend := newBackend().(*linuxBackend)
	if err := backend.PrepareSupervisor(); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("/bin/sh", "-c", "sleep 1 >/dev/null 2>&1 & echo $!").Output()
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		process, readErr := backend.readProcess(child)
		if readErr == nil && process.parent == os.Getpid() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("orphan %d was not adopted: process=%+v err=%v", child, process, readErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(1100 * time.Millisecond)
	var status syscall.WaitStatus
	waited, err := syscall.Wait4(child, &status, 0, nil)
	if err != nil || waited != child || !status.Exited() {
		t.Fatalf("reap orphan: pid=%d status=%v err=%v", waited, status, err)
	}
}

func TestLinuxRootCrashKillsCachedOrphanDescendant(t *testing.T) {
	backend := newBackend().(*linuxBackend)
	if err := backend.PrepareSupervisor(); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	childFile := filepath.Join(directory, "child.pid")
	release := filepath.Join(directory, "release.fifo")
	if err := syscall.Mkfifo(release, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", `sleep 60 & echo $! > "$1"; read ignored < "$2"`, "sh", childFile, release)
	if err := backend.ConfigureProcess(cmd, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backend.SignalGroup(cmd.Process.Pid, true); _ = cmd.Wait() }()
	var child int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(childFile); err == nil {
			child, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			if child > 0 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if child == 0 {
		t.Fatal("child PID was not recorded")
	}
	if _, err := backend.ProcessInfo(context.Background(), cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(release, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = writer.Write([]byte("release\n"))
	_ = writer.Close()
	_ = cmd.Wait()
	_, _ = backend.ProcessInfo(context.Background(), cmd.Process.Pid)
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(child))); os.IsNotExist(err) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("orphan descendant %d survived root crash cleanup", child)
}

func TestLinuxOrphanCleanupRejectsReusedPIDIdentity(t *testing.T) {
	backend := newBackend().(*linuxBackend)
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	process, err := backend.readProcess(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	const missingRoot = 1 << 29
	backend.descendants[missingRoot] = []cachedLinuxDescendant{{pid: cmd.Process.Pid, startTick: process.startTick + 1}}
	backend.reapCachedOrphans(missingRoot)
	time.Sleep(100 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("PID with a different start tick was killed: %v", err)
	}
}

func TestLinuxPidfdSignal(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := signalPIDFD(cmd.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("SIGKILL unexpectedly returned a successful process exit")
	}
}
