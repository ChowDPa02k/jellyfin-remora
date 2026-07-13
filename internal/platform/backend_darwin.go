//go:build darwin

package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"howett.net/plist"
)

type darwinBackend struct{}

func newBackend() Backend { return &darwinBackend{} }

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", filepath.Base(name), err, strings.TrimSpace(string(b)))
	}
	return b, nil
}

func (d *darwinBackend) Mounts(ctx context.Context) ([]MountInfo, error) {
	b, err := run(ctx, "/sbin/mount")
	if err != nil {
		return nil, err
	}
	var out []MountInfo
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		on := strings.Index(line, " on ")
		open := strings.LastIndex(line, " (")
		if on < 0 || open < on {
			continue
		}
		parts := strings.Split(strings.TrimSuffix(line[open+2:], ")"), ", ")
		mi := MountInfo{Source: line[:on], Target: line[on+4 : open]}
		if len(parts) > 0 {
			mi.FSType = parts[0]
			mi.Options = strings.Join(parts[1:], ",")
		}
		out = append(out, mi)
	}
	return out, nil
}

func (d *darwinBackend) ResolvePhysical(ctx context.Context, disk config.DiskConfig) (string, error) {
	identifier := disk.Device
	if identifier == "" {
		identifier = disk.UUID
	}
	b, err := run(ctx, "/usr/sbin/diskutil", "info", "-plist", identifier)
	if err != nil {
		return "", err
	}
	var info struct {
		DeviceNode string `plist:"DeviceNode"`
		VolumeUUID string `plist:"VolumeUUID"`
		Mounted    bool   `plist:"Mounted"`
		MountPoint string `plist:"MountPoint"`
	}
	if _, err := plist.Unmarshal(b, &info); err != nil {
		return "", fmt.Errorf("decode diskutil info: %w", err)
	}
	if disk.UUID != "" && !strings.EqualFold(info.VolumeUUID, disk.UUID) {
		return "", fmt.Errorf("volume UUID mismatch: got %s", info.VolumeUUID)
	}
	if info.DeviceNode == "" {
		return "", errors.New("diskutil did not return a device node")
	}
	return info.DeviceNode, nil
}

func (d *darwinBackend) Mount(ctx context.Context, disk config.DiskConfig) error {
	switch disk.Type {
	case "physical":
		if err := ensureMountTarget(disk.Target); err != nil {
			return err
		}
		device, err := d.ResolvePhysical(ctx, disk)
		if err != nil {
			return err
		}
		args := []string{"mount", "-mountPoint", disk.Target}
		if disk.Permission == "r" {
			args = []string{"mount", "readOnly", "-mountPoint", disk.Target}
		}
		args = append(args, device)
		_, err = run(ctx, "/usr/sbin/diskutil", args...)
		return err
	case "smb":
		if err := ensureMountTarget(disk.Target); err != nil {
			return err
		}
		source, err := smbSource(disk)
		if err != nil {
			return err
		}
		args := []string{"-t", "smbfs"}
		if disk.Options != "" {
			args = append(args, "-o", disk.Options)
		}
		if disk.Permission == "r" {
			args = append(args, "-o", "rdonly")
		}
		args = append(args, source, disk.Target)
		_, err = run(ctx, "/sbin/mount", args...)
		return err
	case "nfs":
		if err := ensureMountTarget(disk.Target); err != nil {
			return err
		}
		source := strings.TrimPrefix(disk.Device, "//")
		if !strings.Contains(source, ":") {
			i := strings.IndexByte(source, '/')
			if i < 1 {
				return fmt.Errorf("invalid NFS source %q; expected server:/path", disk.Device)
			}
			source = source[:i] + ":" + source[i:]
		}
		args := []string{"-t", "nfs"}
		options := disk.Options
		if disk.Permission == "r" {
			if options != "" {
				options += ","
			}
			options += "rdonly"
		}
		if options != "" {
			args = append(args, "-o", options)
		}
		args = append(args, source, disk.Target)
		_, err := run(ctx, "/sbin/mount", args...)
		return err
	default:
		return fmt.Errorf("unsupported disk type %q", disk.Type)
	}
}

func ensureMountTarget(target string) error {
	if target == "" || !filepath.IsAbs(target) || filepath.Clean(target) == string(filepath.Separator) {
		return fmt.Errorf("invalid mount target %q", target)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("create mount target %s: %w", target, err)
	}
	info, err := os.Lstat(target)
	if err != nil {
		return fmt.Errorf("inspect mount target %s: %w", target, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("mount target %s is not a directory", target)
	}
	return nil
}

func smbSource(disk config.DiskConfig) (string, error) {
	raw := strings.TrimPrefix(disk.Device, "//")
	if raw == "" {
		return "", errors.New("empty SMB source")
	}
	if disk.User == "" {
		return "//" + raw, nil
	}
	credentials := url.User(disk.User).String()
	if disk.Password != "" {
		credentials = url.UserPassword(disk.User, disk.Password).String()
	}
	return "//" + credentials + "@" + raw, nil
}

func (d *darwinBackend) ConfigureProcess(cmd *exec.Cmd, username, groupname string) error {
	attr := &syscall.SysProcAttr{Setpgid: true}
	if username != "" {
		u, err := user.Lookup(username)
		if err != nil {
			return fmt.Errorf("lookup user: %w", err)
		}
		uid, _ := strconv.ParseUint(u.Uid, 10, 32)
		gidText := u.Gid
		if groupname != "" {
			g, err := user.LookupGroup(groupname)
			if err != nil {
				return fmt.Errorf("lookup group: %w", err)
			}
			gidText = g.Gid
		}
		gid, _ := strconv.ParseUint(gidText, 10, 32)
		groupIDs, _ := u.GroupIds()
		groups := make([]uint32, 0, len(groupIDs))
		for _, text := range groupIDs {
			value, err := strconv.ParseUint(text, 10, 32)
			if err == nil {
				groups = append(groups, uint32(value))
			}
		}
		if uint64(os.Geteuid()) != uid {
			attr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: groups}
		}
		cmd.Env = replaceEnv(cmd.Env, "HOME", u.HomeDir)
		cmd.Env = replaceEnv(cmd.Env, "USER", u.Username)
		cmd.Env = replaceEnv(cmd.Env, "LOGNAME", u.Username)
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

func (d *darwinBackend) SignalGroup(pid int, force bool) error {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		return err
	}
	if pgid == pid {
		return syscall.Kill(-pgid, sig)
	}
	return syscall.Kill(pid, sig)
}

func (d *darwinBackend) ProcessInfo(ctx context.Context, pid int) (ProcessInfo, error) {
	b, err := run(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-ww", "-o", "pid=,pgid=,state=,%cpu=,rss=,command=")
	if err != nil {
		return ProcessInfo{}, err
	}
	fields := strings.Fields(string(b))
	if len(fields) < 6 {
		return ProcessInfo{}, errors.New("unexpected ps output")
	}
	parsedPID, _ := strconv.Atoi(fields[0])
	pgid, _ := strconv.Atoi(fields[1])
	cpu, _ := strconv.ParseFloat(fields[3], 64)
	rss, _ := strconv.ParseUint(fields[4], 10, 64)
	command := strings.Join(fields[5:], " ")
	return ProcessInfo{PID: parsedPID, PGID: pgid, State: fields[2], CPUPercent: cpu, MemoryBytes: rss * 1024, Command: command, Arguments: splitCommand(command), Ports: d.ports(ctx, parsedPID)}, nil
}

func (d *darwinBackend) FindProcesses(ctx context.Context, executable string, requiredArgs []string) ([]ProcessInfo, error) {
	b, err := run(ctx, "/bin/ps", "-ax", "-ww", "-o", "pid=,command=")
	if err != nil {
		return nil, err
	}
	var out []ProcessInfo
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		cmdline := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		if !strings.HasPrefix(cmdline, executable) {
			continue
		}
		matched := true
		for _, arg := range requiredArgs {
			if !hasRequiredArg(cmdline, arg) {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		pi, err := d.ProcessInfo(ctx, pid)
		if err == nil {
			out = append(out, pi)
		}
	}
	return out, nil
}

func (d *darwinBackend) ports(ctx context.Context, pid int) []int {
	b, err := run(ctx, "/usr/sbin/lsof", "-nP", "-a", "-p", strconv.Itoa(pid), "-iTCP", "-sTCP:LISTEN", "-Fn")
	if err != nil {
		return nil
	}
	seen := map[int]bool{}
	var ports []int
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		s := string(line)
		if !strings.HasPrefix(s, "n") {
			continue
		}
		i := strings.LastIndexByte(s, ':')
		if i < 0 {
			continue
		}
		p, err := strconv.Atoi(strings.TrimSuffix(s[i+1:], " (LISTEN)"))
		if err == nil && !seen[p] {
			seen[p] = true
			ports = append(ports, p)
		}
	}
	return ports
}

func splitCommand(command string) []string { return strings.Fields(command) }
func hasRequiredArg(command, arg string) bool {
	if strings.Contains(command, arg) {
		return true
	}
	parts := strings.SplitN(arg, "=", 2)
	return len(parts) == 2 && strings.Contains(command, parts[0]+" "+parts[1])
}
