//go:build windows

package platform

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

func TestParseWindowsNFSMounts(t *testing.T) {
	output := `
Local    Remote                                 Properties
-------------------------------------------------------------------------------
Z:       \\192.168.1.20\media                  UID=-2, GID=-2
`
	mounts := parseWindowsNFSMounts(output)
	if len(mounts) != 1 || mounts[0].Target != `Z:\` || mounts[0].Source != "192.168.1.20:/media" || mounts[0].FSType != "nfs" {
		t.Fatalf("mounts = %#v", mounts)
	}
}

func TestMergeWindowsNFSMountsOverridesProviderGuess(t *testing.T) {
	mounts := []MountInfo{{Source: "//server/data", Target: `Z:\`, FSType: "smb"}}
	nfs := []MountInfo{{Source: "server:/data", Target: `Z:\`, FSType: "nfs"}}
	got := mergeWindowsNFSMounts(mounts, nfs)
	if len(got) != 1 || got[0] != nfs[0] {
		t.Fatalf("merged mounts = %#v", got)
	}
}

func TestWindowsNFSCommandSource(t *testing.T) {
	for input, want := range map[string]string{
		"server:/exports/media":  `\\server\exports\media`,
		"//server/exports/media": `\\server\exports\media`,
		`\\server\exports\media`: `\\server\exports\media`,
	} {
		if got := windowsNFSCommandSource(input); got != want {
			t.Fatalf("windowsNFSCommandSource(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWindowsNFSOptions(t *testing.T) {
	options, err := windowsNFSOptions("mtype=hard,timeout=2 retry=2,sec=krb5")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(options, "|"); got != "mtype=hard|timeout=2|retry=2|sec=krb5" {
		t.Fatalf("options = %q", got)
	}
	if _, err := windowsNFSOptions("evil-option"); err == nil {
		t.Fatal("unsupported NFS option was accepted")
	}
}

func TestWindowsSMBBackendRejectsPlaintextCredentials(t *testing.T) {
	disk := config.DiskConfig{Type: "smb", Device: "//server/share", Target: `Z:\`, User: "nas"}
	if err := connectNetworkDrive(disk); err == nil || !strings.Contains(err.Error(), "must not be supplied") {
		t.Fatalf("connectNetworkDrive() error = %v", err)
	}
}

func TestWindowsVolumeGUIDForPath(t *testing.T) {
	directory := t.TempDir()
	guid, err := VolumeGUIDForPath(directory)
	if err != nil {
		t.Fatal(err)
	}
	rootGuid, err := volumeNameForMountPoint(filepath.VolumeName(directory) + `\`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(canonicalVolumeGUID(guid), canonicalVolumeGUID(rootGuid)) {
		t.Fatalf("VolumeGUIDForPath(%s) = %s, want %s", directory, guid, rootGuid)
	}
}

func TestWindowsJobObjectTerminatesAttachedProcess(t *testing.T) {
	backend := newBackend().(*windowsBackend)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Start-Sleep -Seconds 30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	if err := backend.AttachProcess(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if err := backend.SignalGroup(cmd.Process.Pid, true); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("attached process survived Job Object termination")
	}
}

func TestWindowsDescendantPIDsIncludesPreexistingTree(t *testing.T) {
	entries := []processEntry32{
		{ProcessID: 100, ParentProcessID: 1},
		{ProcessID: 101, ParentProcessID: 100},
		{ProcessID: 102, ParentProcessID: 101},
		{ProcessID: 200, ParentProcessID: 1},
	}
	if got := windowsDescendantPIDs(entries, 100); !reflect.DeepEqual(got, []int{100, 101, 102}) {
		t.Fatalf("windowsDescendantPIDs() = %v", got)
	}
}

func TestAttachAdoptedProcessTreeRollsBackPartialAssignment(t *testing.T) {
	var attached []int
	rolledBack := false
	injected := errors.New("injected assignment failure")
	err := attachAdoptedProcessTree(
		[]int{100, 101, 102},
		func(pid int) error {
			attached = append(attached, pid)
			if pid == 101 {
				return injected
			}
			return nil
		},
		func() error {
			rolledBack = true
			return nil
		},
	)
	if !errors.Is(err, injected) || !rolledBack {
		t.Fatalf("error=%v rolledBack=%t", err, rolledBack)
	}
	if !reflect.DeepEqual(attached, []int{100, 101}) {
		t.Fatalf("attached=%v", attached)
	}
}

func TestAttachProcessClearsStaleAdoptedPIDClassification(t *testing.T) {
	backend := &windowsBackend{adoptedRoots: map[int]bool{42: true}}
	backend.ProcessExited(42)
	if backend.adoptedRoots[42] {
		t.Fatal("exited adopted PID remained classified as adopted")
	}
}

func TestWindowsProcessCPUIsSampled(t *testing.T) {
	backend := newBackend().(*windowsBackend)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "$end=(Get-Date).AddSeconds(2); while((Get-Date) -lt $end) {}")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	if _, err := backend.ProcessInfo(context.Background(), cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	info, err := backend.ProcessInfo(context.Background(), cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if info.CPUPercent <= 0 {
		t.Fatalf("CPUPercent = %f, want positive sample", info.CPUPercent)
	}
}

func TestParseWindowsTCPPorts(t *testing.T) {
	data := make([]byte, 4+2*24)
	binary.LittleEndian.PutUint32(data[:4], 2)
	binary.LittleEndian.PutUint32(data[4+8:4+12], uint32(0xA01F)) // 8096 in network byte order.
	binary.LittleEndian.PutUint32(data[4+20:4+24], 42)
	binary.LittleEndian.PutUint32(data[28+8:28+12], uint32(0x901F))
	binary.LittleEndian.PutUint32(data[28+20:28+24], 7)
	ports := parseWindowsTCPPorts(data, 24, 8, 20, 42)
	if len(ports) != 1 || ports[0] != 8096 {
		t.Fatalf("ports = %v", ports)
	}
	ipv6 := make([]byte, 4+56)
	binary.LittleEndian.PutUint32(ipv6[:4], 1)
	binary.LittleEndian.PutUint32(ipv6[4+20:4+24], uint32(0xBB01)) // 443 in network byte order.
	binary.LittleEndian.PutUint32(ipv6[4+52:4+56], 42)
	ports = parseWindowsTCPPorts(ipv6, 56, 20, 52, 42)
	if len(ports) != 1 || ports[0] != 443 {
		t.Fatalf("IPv6 ports = %v", ports)
	}
}

func TestWindowsListeningPortsIncludesCurrentProcess(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	want := listener.Addr().(*net.TCPAddr).Port
	backend := newBackend()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		info, infoErr := backend.ProcessInfo(context.Background(), os.Getpid())
		if infoErr != nil {
			t.Fatal(infoErr)
		}
		for _, port := range info.Ports {
			if port == want {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("listener port %d absent from Windows owner-PID TCP table", want)
}
