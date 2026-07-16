//go:build linux

package platform

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"golang.org/x/sys/unix"
)

type linuxBackend struct {
	procRoot      string
	cgroupRoot    string
	clockHz       int64
	mu            sync.Mutex
	descendants   map[int][]cachedLinuxDescendant
	mountMu       sync.Mutex
	pendingMounts map[string]*pendingLinuxCommand
}

type cachedLinuxDescendant struct {
	pid       int
	startTick uint64
}

type pendingLinuxCommand struct {
	done   chan struct{}
	err    error
	output []byte
}

type linuxProcess struct {
	pid       int
	parent    int
	group     int
	state     string
	userTicks uint64
	startTick uint64
	rssPages  int64
	command   string
	args      []string
}

var lookupLinuxSecret = func(ctx context.Context, key string) ([]byte, error) {
	return exec.CommandContext(ctx, "secret-tool", "lookup", "service", "jellyfin-remora", "credential", key).Output()
}

var killLinuxMountProcessGroup = func(pid int) error {
	if err := unix.Kill(-pid, unix.SIGKILL); err != nil {
		process, findErr := os.FindProcess(pid)
		if findErr != nil {
			return err
		}
		return process.Kill()
	}
	return nil
}

func newBackend() Backend {
	return &linuxBackend{
		procRoot: "/proc", cgroupRoot: "/sys/fs/cgroup", clockHz: linuxClockTicks(),
		descendants: make(map[int][]cachedLinuxDescendant), pendingMounts: make(map[string]*pendingLinuxCommand),
	}
}

func linuxClockTicks() int64 {
	output, err := exec.Command("getconf", "CLK_TCK").Output()
	if err == nil {
		if value, parseErr := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64); parseErr == nil && value > 0 {
			return value
		}
	}
	// Linux exposes process times in USER_HZ and every currently supported
	// Jellyfin Linux architecture uses 100 when getconf is unavailable.
	return 100
}

func (l *linuxBackend) PrepareSupervisor() error {
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("enable child subreaper: %w", err)
	}
	return nil
}

func (*linuxBackend) ExecutableProvenance(string) (bool, error) { return false, nil }

func (l *linuxBackend) Mounts(context.Context) ([]MountInfo, error) {
	data, err := os.ReadFile(filepath.Join(l.procRoot, "self", "mountinfo"))
	if err != nil {
		return nil, fmt.Errorf("read mountinfo: %w", err)
	}
	return parseLinuxMountInfo(data)
}

func parseLinuxMountInfo(data []byte) ([]MountInfo, error) {
	var mounts []MountInfo
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		left, right, ok := strings.Cut(line, " - ")
		if !ok {
			return nil, fmt.Errorf("malformed mountinfo line %q", line)
		}
		before := strings.Fields(left)
		after := strings.Fields(right)
		if len(before) < 6 || len(after) < 3 {
			return nil, fmt.Errorf("malformed mountinfo line %q", line)
		}
		source := decodeMountInfoField(after[1])
		if strings.HasPrefix(source, "/dev/") {
			if resolved, resolveErr := filepath.EvalSymlinks(source); resolveErr == nil {
				source = resolved
			}
		}
		options := before[5]
		if after[2] != "" {
			options += "," + after[2]
		}
		mounts = append(mounts, MountInfo{
			Source: source, Target: decodeMountInfoField(before[4]),
			FSType: after[0], Options: options,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan mountinfo: %w", err)
	}
	return mounts, nil
}

func decodeMountInfoField(value string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(value)
}

func (*linuxBackend) ResolvePhysical(_ context.Context, disk config.DiskConfig) (string, error) {
	path := disk.Device
	if disk.UUID != "" {
		path = filepath.Join("/dev/disk/by-uuid", disk.UUID)
	}
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("invalid physical device %q", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve physical device %s: %w", path, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect physical device %s: %w", resolved, err)
	}
	if info.Mode()&os.ModeDevice == 0 || info.Mode()&os.ModeCharDevice != 0 {
		return "", fmt.Errorf("physical device %s is not a block device", resolved)
	}
	return resolved, nil
}

func (l *linuxBackend) Mount(ctx context.Context, disk config.DiskConfig) error {
	if err := ensureLinuxMountTarget(disk.Target); err != nil {
		return err
	}
	switch disk.Type {
	case "physical":
		device, err := l.ResolvePhysical(ctx, disk)
		if err != nil {
			return err
		}
		args := linuxMountOptions(disk, nil)
		args = append(args, device, disk.Target)
		return l.runMount(ctx, disk.Target, "/usr/bin/mount", args, nil)
	case "nfs":
		source := normalizeLinuxNFSSource(disk.Device)
		if !strings.Contains(source, ":") {
			return fmt.Errorf("invalid NFS source %q; expected server:/export", disk.Device)
		}
		args := []string{"-t", "nfs"}
		args = append(args, linuxMountOptions(disk, []string{"soft", "timeo=50", "retrans=2"})...)
		args = append(args, source, disk.Target)
		return l.runMount(ctx, disk.Target, "/usr/bin/mount", args, nil)
	case "smb":
		if disk.Device == "" {
			return errors.New("empty SMB source")
		}
		credentialOption, files, cleanup, err := linuxSMBCredential(ctx, disk.Credential)
		if err != nil {
			return err
		}
		defer cleanup()
		extra := []string{"nosuid", "nodev", "soft", "echo_interval=15"}
		if credentialOption != "" {
			extra = append(extra, credentialOption)
		}
		args := linuxMountOptions(disk, extra)
		args = append(args, "//"+strings.TrimPrefix(disk.Device, "//"), disk.Target)
		return l.runMount(ctx, disk.Target, "/usr/sbin/mount.cifs", args, files)
	default:
		return fmt.Errorf("unsupported disk type %q", disk.Type)
	}
}

func ensureLinuxMountTarget(target string) error {
	if target == "" || !filepath.IsAbs(target) || filepath.Clean(target) == "/" {
		return fmt.Errorf("invalid mount target %q", target)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("create mount target %s: %w", target, err)
	}
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("mount target %s is not a real directory", target)
	}
	return nil
}

func linuxMountOptions(disk config.DiskConfig, defaults []string) []string {
	configured := splitLinuxMountOptions(disk.Options)
	options := make([]string, 0, len(defaults)+len(configured)+1)
	for _, option := range defaults {
		if !linuxMountOptionOverridden(option, configured) {
			options = append(options, option)
		}
	}
	options = append(options, configured...)
	if disk.Permission == "r" {
		if !linuxMountOptionOverridden("ro", configured) {
			options = append(options, "ro")
		}
	}
	if len(options) == 0 {
		return nil
	}
	return []string{"-o", strings.Join(options, ",")}
}

func splitLinuxMountOptions(value string) []string {
	var options []string
	for _, option := range strings.Split(value, ",") {
		if option = strings.TrimSpace(option); option != "" {
			options = append(options, option)
		}
	}
	return options
}

func linuxMountOptionOverridden(defaultOption string, configured []string) bool {
	key, _, _ := strings.Cut(defaultOption, "=")
	for _, option := range configured {
		configuredKey, _, _ := strings.Cut(option, "=")
		if configuredKey == key {
			return true
		}
		switch key {
		case "soft", "hard":
			if configuredKey == "soft" || configuredKey == "hard" {
				return true
			}
		case "ro", "rw":
			if configuredKey == "ro" || configuredKey == "rw" {
				return true
			}
		}
	}
	return false
}

func normalizeLinuxNFSSource(source string) string {
	source = strings.TrimPrefix(source, "//")
	if strings.Contains(source, ":") {
		return source
	}
	if index := strings.IndexByte(source, '/'); index > 0 {
		return source[:index] + ":" + source[index:]
	}
	return source
}

func (l *linuxBackend) runMount(ctx context.Context, target, path string, args []string, extraFiles []*os.File) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s timed out: %w", filepath.Base(path), err)
	}
	if processes, err := l.FindProcesses(ctx, path, []string{target}); err == nil && len(processes) > 0 {
		return fmt.Errorf("previous mount helper for %s remains blocked", target)
	}
	l.mountMu.Lock()
	if l.pendingMounts == nil {
		l.pendingMounts = make(map[string]*pendingLinuxCommand)
	}
	if previous := l.pendingMounts[target]; previous != nil {
		select {
		case <-previous.done:
			delete(l.pendingMounts, target)
		default:
			l.mountMu.Unlock()
			return fmt.Errorf("previous mount helper for %s remains blocked", target)
		}
	}
	cmd := exec.Command(path, args...)
	cmd.ExtraFiles = extraFiles
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		l.mountMu.Unlock()
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	pending := &pendingLinuxCommand{done: make(chan struct{})}
	l.pendingMounts[target] = pending
	l.mountMu.Unlock()
	go func() {
		pending.err = cmd.Wait()
		pending.output = append([]byte(nil), output.Bytes()...)
		close(pending.done)
	}()

	select {
	case <-pending.done:
		l.mountMu.Lock()
		if l.pendingMounts[target] == pending {
			delete(l.pendingMounts, target)
		}
		l.mountMu.Unlock()
		if pending.err != nil {
			return fmt.Errorf("%s: %w: %s", filepath.Base(path), pending.err, strings.TrimSpace(string(pending.output)))
		}
		return nil
	case <-ctx.Done():
		_ = killLinuxMountProcessGroup(cmd.Process.Pid)
		return fmt.Errorf("%s timed out: %w", filepath.Base(path), ctx.Err())
	}
}

func linuxSMBCredential(ctx context.Context, reference string) (string, []*os.File, func(), error) {
	if reference == "" {
		return "", nil, func() {}, nil
	}
	if strings.HasPrefix(reference, "libsecret:") {
		key := strings.TrimSpace(strings.TrimPrefix(reference, "libsecret:"))
		output, err := lookupLinuxSecret(ctx, key)
		if err != nil {
			return "", nil, func() {}, fmt.Errorf("read libsecret credential %q: %w", key, err)
		}
		if !bytes.Contains(output, []byte("username=")) || !bytes.Contains(output, []byte("password=")) {
			return "", nil, func() {}, fmt.Errorf("libsecret credential %q must contain mount.cifs username= and password= lines", key)
		}
		file, err := anonymousCredentialFile(output)
		if err != nil {
			return "", nil, func() {}, err
		}
		return "credentials=/proc/self/fd/3", []*os.File{file}, func() { _ = file.Close() }, nil
	}
	path := strings.TrimPrefix(reference, "file:")
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", nil, func() {}, fmt.Errorf("open SMB credential file: %w", err)
	}
	file := os.NewFile(uintptr(fd), filepath.Base(path))
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = file.Close()
		return "", nil, func() {}, fmt.Errorf("inspect SMB credential file: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o077 != 0 {
		_ = file.Close()
		return "", nil, func() {}, fmt.Errorf("SMB credential file %s must be regular and inaccessible to group/other", path)
	}
	if stat.Uid != uint32(os.Geteuid()) {
		_ = file.Close()
		return "", nil, func() {}, fmt.Errorf("SMB credential file %s must be owned by uid %d", path, os.Geteuid())
	}
	return "credentials=/proc/self/fd/3", []*os.File{file}, func() { _ = file.Close() }, nil
}

func anonymousCredentialFile(contents []byte) (*os.File, error) {
	fd, err := unix.MemfdCreate("remora-cifs-credential", unix.MFD_CLOEXEC)
	if err != nil {
		return nil, fmt.Errorf("create in-memory SMB credential: %w", err)
	}
	file := os.NewFile(uintptr(fd), "remora-cifs-credential")
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return nil, err
	}
	if _, err := file.Seek(0, 0); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func (l *linuxBackend) ConfigureProcess(cmd *exec.Cmd, username, groupname string) error {
	attr := &syscall.SysProcAttr{Setpgid: true}
	if username != "" {
		account, err := user.Lookup(username)
		if err != nil {
			return fmt.Errorf("lookup user %s: %w", username, err)
		}
		uid, err := strconv.ParseUint(account.Uid, 10, 32)
		if err != nil {
			return fmt.Errorf("parse UID for %s: %w", username, err)
		}
		gidText := account.Gid
		if groupname != "" {
			group, err := user.LookupGroup(groupname)
			if err != nil {
				return fmt.Errorf("lookup group %s: %w", groupname, err)
			}
			gidText = group.Gid
		}
		gid, err := strconv.ParseUint(gidText, 10, 32)
		if err != nil {
			return fmt.Errorf("parse GID for %s: %w", username, err)
		}
		var groups []uint32
		if ids, groupErr := account.GroupIds(); groupErr == nil {
			for _, text := range ids {
				if value, parseErr := strconv.ParseUint(text, 10, 32); parseErr == nil {
					groups = append(groups, uint32(value))
				}
			}
		}
		if uint64(os.Geteuid()) != uid || uint64(os.Getegid()) != gid {
			attr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: groups}
		}
		cmd.Env = replaceEnv(cmd.Env, "HOME", account.HomeDir)
		cmd.Env = replaceEnv(cmd.Env, "USER", account.Username)
		cmd.Env = replaceEnv(cmd.Env, "LOGNAME", account.Username)
	}
	cmd.SysProcAttr = attr
	return nil
}

func replaceEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			if !replaced {
				out = append(out, prefix+value)
				replaced = true
			}
			continue
		}
		out = append(out, entry)
	}
	if !replaced {
		out = append(out, prefix+value)
	}
	return out
}

func sameExecutable(actual, expected string) bool {
	actual = strings.TrimSuffix(actual, " (deleted)")
	actualInfo, actualErr := os.Stat(actual)
	expectedInfo, expectedErr := os.Stat(expected)
	if actualErr == nil && expectedErr == nil {
		return os.SameFile(actualInfo, expectedInfo)
	}
	actualResolved, actualErr := filepath.EvalSymlinks(actual)
	expectedResolved, expectedErr := filepath.EvalSymlinks(expected)
	return actualErr == nil && expectedErr == nil && actualResolved == expectedResolved
}

func (l *linuxBackend) SignalGroup(pid int, force bool) error {
	signal := unix.SIGTERM
	if force {
		signal = unix.SIGKILL
	}
	processes, _ := l.processSnapshot()
	descendants := l.managedProcessPIDs(processes, pid)
	pgid, err := unix.Getpgid(pid)
	if err != nil {
		return err
	}
	if pgid == pid {
		err = unix.Kill(-pgid, signal)
	} else {
		err = signalPIDFD(pid, signal)
	}
	if err != nil && !errors.Is(err, unix.ESRCH) {
		return err
	}
	for _, child := range descendants {
		if child == pid || child == os.Getpid() {
			continue
		}
		childGroup, groupErr := unix.Getpgid(child)
		if groupErr == nil && childGroup == pgid {
			continue
		}
		if childErr := signalPIDFD(child, signal); childErr != nil && !errors.Is(childErr, unix.ESRCH) {
			return fmt.Errorf("signal descendant %d: %w", child, childErr)
		}
	}
	return nil
}

func signalPIDFD(pid int, signal unix.Signal) error {
	fd, err := unix.PidfdOpen(pid, 0)
	if err == nil {
		defer unix.Close(fd)
		return unix.PidfdSendSignal(fd, signal, nil, 0)
	}
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EPERM) {
		return unix.Kill(pid, signal)
	}
	return err
}

func (l *linuxBackend) ProcessInfo(_ context.Context, pid int) (ProcessInfo, error) {
	process, err := l.readProcess(pid)
	if err != nil {
		l.reapCachedOrphans(pid)
		return ProcessInfo{}, err
	}
	boot, err := l.bootTime()
	if err != nil {
		return ProcessInfo{}, err
	}
	startedAt := boot.Add(time.Duration(process.startTick) * time.Second / time.Duration(l.clockHz))
	elapsed := time.Since(startedAt).Seconds()
	cpu := 0.0
	if elapsed > 0 {
		cpu = (float64(process.userTicks) / float64(l.clockHz)) / elapsed * 100
	}
	snapshot, _ := l.processSnapshot()
	managed := l.managedProcessPIDs(snapshot, pid)
	cached := make([]cachedLinuxDescendant, 0, len(managed))
	for _, managedPID := range managed {
		if process, exists := snapshot[managedPID]; exists {
			cached = append(cached, cachedLinuxDescendant{pid: managedPID, startTick: process.startTick})
		}
	}
	l.mu.Lock()
	l.descendants[pid] = cached
	l.mu.Unlock()
	return ProcessInfo{
		PID: pid, PGID: process.group, State: process.state,
		Command: process.command, Arguments: append([]string(nil), process.args...),
		CPUPercent: cpu, MemoryBytes: uint64(max(int64(0), process.rssPages)) * uint64(os.Getpagesize()),
		FFmpegProcesses: l.countFFmpeg(snapshot, pid), Ports: l.listeningPorts(pid), StartedAt: startedAt,
	}, nil
}

// ProcessExited is called by procmanager after wait4 has reaped a Jellyfin
// process started by Remora. Adopted processes have no local exec.Cmd waiter
// and reach the same cleanup through the ProcessInfo error path above.
func (l *linuxBackend) ProcessExited(pid int) {
	l.reapCachedOrphans(pid)
}

func (l *linuxBackend) reapCachedOrphans(root int) {
	l.mu.Lock()
	descendants := append([]cachedLinuxDescendant(nil), l.descendants[root]...)
	delete(l.descendants, root)
	l.mu.Unlock()
	seen := make(map[int]bool, len(descendants))
	for _, cached := range descendants {
		seen[cached.pid] = true
	}
	// A process can fork and crash before the first supervisor sample. Processes
	// that retained Jellyfin's process group are attributable even without
	// cgroup v2. Under Remora's systemd unit, supplement that set from the
	// service cgroup as well, while excluding Remora's own concurrent helpers.
	if snapshot, err := l.processSnapshot(); err == nil {
		cgroupMembers := make(map[int]bool)
		for _, pid := range l.remoraCgroupPIDs(os.Getpid()) {
			cgroupMembers[pid] = true
		}
		for pid, process := range snapshot {
			if seen[pid] || !shouldSupplementLinuxOrphan(root, os.Getpid(), process, cgroupMembers[pid]) {
				continue
			}
			descendants = append(descendants, cachedLinuxDescendant{pid: pid, startTick: process.startTick})
			seen[pid] = true
		}
	}
	if len(descendants) == 0 {
		return
	}
	cleanup := make([]int, 0, len(descendants))
	for _, cached := range descendants {
		if cached.pid == root || cached.pid == os.Getpid() {
			continue
		}
		process, err := l.readProcess(cached.pid)
		if err != nil || process.startTick != cached.startTick || process.parent != os.Getpid() && !l.inRemoraServiceCgroup(cached.pid) {
			continue
		}
		_ = signalPIDFD(cached.pid, unix.SIGKILL)
		cleanup = append(cleanup, cached.pid)
	}
	if len(cleanup) == 0 {
		return
	}
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		pending := make(map[int]bool)
		for _, pid := range cleanup {
			if pid != root && pid != os.Getpid() {
				pending[pid] = true
			}
		}
		for len(pending) > 0 && time.Now().Before(deadline) {
			for pid := range pending {
				process, err := l.readProcess(pid)
				if errors.Is(err, fs.ErrNotExist) {
					delete(pending, pid)
					continue
				}
				if err != nil || process.parent != os.Getpid() {
					continue
				}
				var status unix.WaitStatus
				waited, waitErr := unix.Wait4(pid, &status, unix.WNOHANG, nil)
				if waited == pid || errors.Is(waitErr, unix.ECHILD) {
					delete(pending, pid)
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
}

func shouldSupplementLinuxOrphan(root, supervisorPID int, process linuxProcess, inServiceCgroup bool) bool {
	if process.pid <= 0 || process.pid == root || process.pid == supervisorPID {
		return false
	}
	if process.group == root {
		return true
	}
	if !inServiceCgroup {
		return false
	}
	// Storage probes share the service cgroup but are direct Remora helpers,
	// not members of Jellyfin's process tree. Killing one here can turn an
	// ordinary Jellyfin crash into a false storage fence.
	return !isLinuxInternalProbe(process, supervisorPID)
}

func isLinuxInternalProbe(process linuxProcess, supervisorPID int) bool {
	if process.parent != supervisorPID || len(process.args) < 2 || process.args[1] != "internal-probe" {
		return false
	}
	name := strings.ToLower(filepath.Base(strings.TrimSuffix(process.command, " (deleted)")))
	return strings.Contains(name, "jellyfin-remora")
}

func (l *linuxBackend) inRemoraServiceCgroup(pid int) bool {
	data, err := os.ReadFile(filepath.Join(l.procRoot, strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "0::") && cgroupContainsRemoraService(strings.TrimPrefix(line, "0::")) {
			return true
		}
	}
	return false
}

func (l *linuxBackend) FindProcesses(ctx context.Context, executable string, requiredArgs []string) ([]ProcessInfo, error) {
	entries, err := os.ReadDir(l.procRoot)
	if err != nil {
		return nil, err
	}
	var matches []ProcessInfo
	for _, entry := range entries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		actual, err := os.Readlink(filepath.Join(l.procRoot, entry.Name(), "exe"))
		if err != nil || !sameExecutable(actual, executable) {
			continue
		}
		args, err := l.readArguments(pid)
		if err != nil || !containsRequiredArguments(args, requiredArgs) {
			continue
		}
		info, err := l.ProcessInfo(ctx, pid)
		if err == nil {
			matches = append(matches, info)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].PID < matches[j].PID })
	return matches, nil
}

func (l *linuxBackend) readProcess(pid int) (linuxProcess, error) {
	data, err := os.ReadFile(filepath.Join(l.procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return linuxProcess{}, err
	}
	process, err := parseLinuxProcStat(data)
	if err != nil {
		return linuxProcess{}, fmt.Errorf("parse /proc/%d/stat: %w", pid, err)
	}
	process.pid = pid
	process.args, err = l.readArguments(pid)
	if err != nil && !errors.Is(err, fs.ErrPermission) {
		return linuxProcess{}, err
	}
	if path, pathErr := os.Readlink(filepath.Join(l.procRoot, strconv.Itoa(pid), "exe")); pathErr == nil {
		process.command = path
	} else if len(process.args) > 0 {
		process.command = process.args[0]
	}
	return process, nil
}

func parseLinuxProcStat(data []byte) (linuxProcess, error) {
	closeIndex := bytes.LastIndexByte(data, ')')
	openIndex := bytes.IndexByte(data, '(')
	if openIndex < 0 || closeIndex <= openIndex || closeIndex+2 > len(data) {
		return linuxProcess{}, errors.New("missing command boundaries")
	}
	fields := strings.Fields(string(data[closeIndex+2:]))
	if len(fields) < 22 {
		return linuxProcess{}, errors.New("too few fields")
	}
	parseInt := func(index int) (int64, error) { return strconv.ParseInt(fields[index], 10, 64) }
	parent, err := parseInt(1)
	if err != nil {
		return linuxProcess{}, err
	}
	group, err := parseInt(2)
	if err != nil {
		return linuxProcess{}, err
	}
	utime, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return linuxProcess{}, err
	}
	stime, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return linuxProcess{}, err
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return linuxProcess{}, err
	}
	rss, err := parseInt(21)
	if err != nil {
		return linuxProcess{}, err
	}
	return linuxProcess{parent: int(parent), group: int(group), state: fields[0], userTicks: utime + stime, startTick: start, rssPages: rss, command: string(data[openIndex+1 : closeIndex])}, nil
}

func (l *linuxBackend) readArguments(pid int) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(l.procRoot, strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil, err
	}
	data = bytes.TrimRight(data, "\x00")
	if len(data) == 0 {
		return nil, nil
	}
	parts := bytes.Split(data, []byte{0})
	arguments := make([]string, 0, len(parts))
	for _, part := range parts {
		arguments = append(arguments, string(part))
	}
	return arguments, nil
}

func containsRequiredArguments(arguments, required []string) bool {
	for _, wanted := range required {
		matched := false
		for index, argument := range arguments {
			if argument == wanted {
				matched = true
				break
			}
			key, value, hasValue := strings.Cut(wanted, "=")
			if hasValue && argument == key && index+1 < len(arguments) && arguments[index+1] == value {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func (l *linuxBackend) processSnapshot() (map[int]linuxProcess, error) {
	entries, err := os.ReadDir(l.procRoot)
	if err != nil {
		return nil, err
	}
	processes := make(map[int]linuxProcess)
	for _, entry := range entries {
		pid, parseErr := strconv.Atoi(entry.Name())
		if parseErr != nil {
			continue
		}
		process, readErr := l.readProcess(pid)
		if readErr == nil {
			processes[pid] = process
		}
	}
	return processes, nil
}

func descendantPIDs(processes map[int]linuxProcess, root int) []int {
	descendants := map[int]bool{root: true}
	for changed := true; changed; {
		changed = false
		for pid, process := range processes {
			if descendants[process.parent] && !descendants[pid] {
				descendants[pid] = true
				changed = true
			}
		}
	}
	result := make([]int, 0, len(descendants))
	for pid := range descendants {
		result = append(result, pid)
	}
	sort.Ints(result)
	return result
}

func (l *linuxBackend) countFFmpeg(processes map[int]linuxProcess, root int) int {
	descendants := l.managedProcessPIDs(processes, root)
	count := 0
	for _, pid := range descendants {
		if pid == root {
			continue
		}
		process := processes[pid]
		name := strings.ToLower(filepath.Base(process.command))
		if strings.Contains(name, "ffmpeg") {
			count++
		}
	}
	return count
}

// managedProcessPIDs uses ancestry on every Linux system. Under the generated
// systemd unit it also merges the cgroup v2 membership, which keeps accounting
// correct when ffmpeg daemonizes or changes its process group. Cgroup expansion
// is deliberately restricted to Remora's own unit so an interactive invocation
// never mistakes unrelated processes in a user/session cgroup for descendants.
func (l *linuxBackend) managedProcessPIDs(processes map[int]linuxProcess, root int) []int {
	managed := make(map[int]bool)
	for _, pid := range descendantPIDs(processes, root) {
		managed[pid] = true
	}
	for _, pid := range l.remoraCgroupPIDs(root) {
		if _, exists := processes[pid]; exists {
			managed[pid] = true
		}
	}
	result := make([]int, 0, len(managed))
	for pid := range managed {
		result = append(result, pid)
	}
	sort.Ints(result)
	return result
}

func (l *linuxBackend) remoraCgroupPIDs(pid int) []int {
	data, err := os.ReadFile(filepath.Join(l.procRoot, strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return nil
	}
	var relative string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "0::") {
			relative = strings.TrimPrefix(line, "0::")
			break
		}
	}
	if relative == "" || !cgroupContainsRemoraService(relative) {
		return nil
	}
	root := filepath.Join(l.cgroupRoot, filepath.Clean("/"+relative))
	cleanCgroupRoot := filepath.Clean(l.cgroupRoot)
	if root != cleanCgroupRoot && !strings.HasPrefix(root, cleanCgroupRoot+string(filepath.Separator)) {
		return nil
	}
	seen := make(map[int]bool)
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || entry.Name() != "cgroup.procs" {
			return nil
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for _, field := range strings.Fields(string(contents)) {
			if value, parseErr := strconv.Atoi(field); parseErr == nil && value > 0 {
				seen[value] = true
			}
		}
		return nil
	})
	result := make([]int, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Ints(result)
	return result
}

func cgroupContainsRemoraService(path string) bool {
	for _, component := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if component == "jellyfin-remora.service" {
			return true
		}
	}
	return false
}

func (l *linuxBackend) bootTime() (time.Time, error) {
	file, err := os.Open(filepath.Join(l.procRoot, "stat"))
	if err != nil {
		return time.Time{}, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "btime ") {
			seconds, parseErr := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "btime ")), 10, 64)
			if parseErr != nil {
				return time.Time{}, parseErr
			}
			return time.Unix(seconds, 0), nil
		}
	}
	return time.Time{}, errors.New("/proc/stat does not contain btime")
}

func (l *linuxBackend) listeningPorts(pid int) []int {
	inodes := make(map[string]bool)
	entries, err := os.ReadDir(filepath.Join(l.procRoot, strconv.Itoa(pid), "fd"))
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		target, readErr := os.Readlink(filepath.Join(l.procRoot, strconv.Itoa(pid), "fd", entry.Name()))
		if readErr == nil && strings.HasPrefix(target, "socket:[") && strings.HasSuffix(target, "]") {
			inodes[strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")] = true
		}
	}
	seen := make(map[int]bool)
	var ports []int
	for _, table := range []string{"tcp", "tcp6"} {
		file, openErr := os.Open(filepath.Join(l.procRoot, strconv.Itoa(pid), "net", table))
		if openErr != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 10 || fields[3] != "0A" || !inodes[fields[9]] {
				continue
			}
			_, portHex, ok := strings.Cut(fields[1], ":")
			if !ok {
				continue
			}
			port, parseErr := strconv.ParseInt(portHex, 16, 32)
			if parseErr == nil && !seen[int(port)] {
				seen[int(port)] = true
				ports = append(ports, int(port))
			}
		}
		_ = file.Close()
	}
	sort.Ints(ports)
	return ports
}
