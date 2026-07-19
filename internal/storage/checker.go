package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
)

type Checker struct {
	cfg              *config.Config
	backend          platform.Backend
	executable       string
	probeUsername    string
	probeGroup       string
	failureMu        sync.Mutex
	failureCounts    []int
	confirmedHealthy []bool
	mountMu          sync.RWMutex
	recoveryMounts   bool
	probeMu          sync.Mutex
	pendingProbes    map[string]*pendingProbe
	probeOverride    func(context.Context, string, string) error
}

type pendingProbe struct {
	done   chan struct{}
	err    error
	output []byte
}

func New(cfg *config.Config, backend platform.Backend) (*Checker, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	checker, err := NewWithExecutable(cfg, backend, exe)
	if err != nil {
		return nil, err
	}
	checker.useJellyfinIdentity()
	return checker, nil
}

func NewWithExecutable(cfg *config.Config, backend platform.Backend, executable string) (*Checker, error) {
	if executable == "" {
		return nil, errors.New("storage probe executable is required")
	}
	return &Checker{cfg: cfg, backend: backend, executable: executable, failureCounts: make([]int, len(cfg.Disks)), confirmedHealthy: make([]bool, len(cfg.Disks)), recoveryMounts: true, pendingProbes: make(map[string]*pendingProbe)}, nil
}

// NewForInit validates storage using the configured Jellyfin identity. Mount
// operations remain owned by remoractl.
func NewForInit(cfg *config.Config, backend platform.Backend, executable string) (*Checker, error) {
	checker, err := NewWithExecutable(cfg, backend, executable)
	if err != nil {
		return nil, err
	}
	checker.useJellyfinIdentity()
	return checker, nil
}

func (c *Checker) useJellyfinIdentity() {
	if c.cfg.Jellyfin.RunAsUser != "" {
		c.probeUsername = c.cfg.Jellyfin.RunAsUser
		c.probeGroup = c.cfg.Jellyfin.RunAsGroup
	}
}

func (c *Checker) CheckAll(ctx context.Context) []model.StorageResult {
	results := make([]model.StorageResult, len(c.cfg.Disks))
	for i := range c.cfg.Disks {
		results[i] = c.CheckDisk(ctx, i)
	}
	return results
}

func (c *Checker) CheckDisk(ctx context.Context, index int) model.StorageResult {
	if index < 0 || index >= len(c.cfg.Disks) {
		return model.StorageResult{Index: index, Fatal: true, Message: "disk index out of range", CheckedAt: time.Now()}
	}
	disk := c.cfg.Disks[index]
	c.mountMu.RLock()
	allowMount := c.recoveryMounts
	c.mountMu.RUnlock()
	return c.applyFailureThreshold(index, disk, c.checkRaw(ctx, index, disk, allowMount, false))
}

// SetMountRecoveryAllowed separates safe startup/fenced recovery from runtime
// monitoring. A live Jellyfin must be stopped before Remora mounts a replacement
// filesystem at a path whose previous mount disappeared.
func (c *Checker) SetMountRecoveryAllowed(allowed bool) {
	c.mountMu.Lock()
	c.recoveryMounts = allowed
	c.mountMu.Unlock()
}

func (c *Checker) InspectDisk(ctx context.Context, index int) model.StorageResult {
	if index < 0 || index >= len(c.cfg.Disks) {
		return model.StorageResult{Index: index, Fatal: true, Message: "disk index out of range", CheckedAt: time.Now()}
	}
	return c.checkRaw(ctx, index, c.cfg.Disks[index], false, false)
}

// CheckDiskForInit performs the same mount and I/O validation as CheckDisk but
// can continue probing an already-mounted target after the operator explicitly
// accepts a source mismatch. Runtime supervision never uses this exception.
func (c *Checker) CheckDiskForInit(ctx context.Context, index int, allowSourceMismatch bool) model.StorageResult {
	if index < 0 || index >= len(c.cfg.Disks) {
		return model.StorageResult{Index: index, Fatal: true, Message: "disk index out of range", CheckedAt: time.Now()}
	}
	return c.checkRaw(ctx, index, c.cfg.Disks[index], true, allowSourceMismatch)
}

func (c *Checker) CheckPaths(ctx context.Context) []model.StorageResult {
	paths := []string{c.cfg.Jellyfin.DataDir, c.cfg.Jellyfin.ConfigDir, c.cfg.Jellyfin.CacheDir, c.cfg.Jellyfin.LogDir}
	if transcode := c.cfg.Jellyfin.Playback.Transcoding.TranscodePath; transcode.Set && !transcode.Null && transcode.Value != "" {
		paths = append(paths, transcode.Value)
	}
	seen := map[string]bool{}
	results := make([]model.StorageResult, 0, len(paths))
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		r := model.StorageResult{Index: len(c.cfg.Disks) + len(results), Type: "path", Target: path, Mounted: true, Reachable: true, CheckedAt: time.Now()}
		if err := c.probePath(ctx, path, "rw"); err != nil {
			r.Fatal = true
			r.Message = err.Error()
		} else {
			r.Healthy = true
			r.Writable = true
		}
		results = append(results, r)
	}
	return results
}

func (c *Checker) checkRaw(ctx context.Context, index int, disk config.DiskConfig, allowMount, allowSourceMismatch bool) (r model.StorageResult) {
	started := time.Now()
	r = model.StorageResult{Index: index, Type: disk.Type, Device: redactDevice(disk), Target: disk.Target, CheckedAt: started}
	defer func() { r.LatencyMS = time.Since(started).Milliseconds() }()
	mounts, err := c.backend.Mounts(ctx)
	if err != nil {
		r.Fatal = true
		r.Message = err.Error()
		return r
	}
	mi, mounted := findTarget(mounts, disk.Target)
	if !mounted && allowMount {
		mountCtx, cancel := context.WithTimeout(ctx, c.cfg.Remora.IOTimeout.Duration)
		err = c.backend.Mount(mountCtx, disk)
		cancel()
		if err == nil {
			mounts, err = c.backend.Mounts(ctx)
			if err == nil {
				mi, mounted = findTarget(mounts, disk.Target)
			}
		}
	}
	r.Mounted = mounted
	r.Reachable = c.reachable(ctx, disk)
	if !mounted {
		r.Fatal = true
		if err != nil {
			r.Message = "mount failed: " + redact(err.Error(), disk.Password)
		} else {
			r.Message = "target is not mounted"
		}
		return r
	}
	expected, err := c.expectedSource(ctx, disk)
	if err != nil {
		r.Fatal = true
		r.Message = err.Error()
		return r
	}
	sourceEquivalent := sourceMatches(mi, disk.Type, expected)
	if !sourceEquivalent && disk.Type == "smb" {
		lookupCtx, cancel := context.WithTimeout(ctx, c.cfg.Remora.IOTimeout.Duration)
		sourceEquivalent = smbSourcesEquivalent(lookupCtx, mi.Source, expected)
		cancel()
	}
	if !sourceEquivalent {
		r.Message = fmt.Sprintf("mount source mismatch: got %s", mi.Source)
		if !allowSourceMismatch {
			r.Fatal = true
			return r
		}
	}
	probePath := disk.ProbePath
	if probePath == "" {
		probePath = disk.Target
	}
	if err := c.probePath(ctx, probePath, disk.Permission); err != nil {
		r.Fatal = true
		r.Message = err.Error()
		return r
	}
	r.Writable = disk.Permission == "rw"
	r.Healthy = true
	if !r.Reachable && (disk.Type == "smb" || disk.Type == "nfs") {
		r.Healthy = false
		r.Fatal = false
		if r.Message != "" {
			r.Message += "; "
		}
		r.Message += "server port unreachable while mounted I/O remains healthy"
	}
	return r
}

func (c *Checker) applyFailureThreshold(index int, disk config.DiskConfig, result model.StorageResult) model.StorageResult {
	c.failureMu.Lock()
	defer c.failureMu.Unlock()
	for len(c.failureCounts) <= index {
		c.failureCounts = append(c.failureCounts, 0)
		c.confirmedHealthy = append(c.confirmedHealthy, false)
	}
	threshold := max(1, disk.FailureThreshold)
	if !result.Fatal {
		c.failureCounts[index] = 0
		if result.Healthy {
			c.confirmedHealthy[index] = true
		}
		return result
	}
	if !c.confirmedHealthy[index] || threshold == 1 {
		return result
	}
	c.failureCounts[index]++
	if c.failureCounts[index] >= threshold {
		return result
	}
	result.Fatal = false
	result.Message = fmt.Sprintf("%s (failure %d/%d; waiting for failure threshold)", result.Message, c.failureCounts[index], threshold)
	return result
}

func (c *Checker) probePath(ctx context.Context, path, permission string) error {
	if c.probeOverride != nil {
		return c.probeOverride(ctx, path, permission)
	}
	cmd := exec.Command(c.executable, "internal-probe", "--path", path, "--permission", permission)
	if c.probeUsername != "" {
		if err := c.backend.ConfigureProcess(cmd, c.probeUsername, c.probeGroup); err != nil {
			return fmt.Errorf("configure storage probe identity: %w", err)
		}
	}
	key := c.probeUsername + "\x00" + c.probeGroup + "\x00" + permission + "\x00" + path
	c.probeMu.Lock()
	if c.pendingProbes == nil {
		c.pendingProbes = make(map[string]*pendingProbe)
	}
	if previous := c.pendingProbes[key]; previous != nil {
		select {
		case <-previous.done:
			delete(c.pendingProbes, key)
		default:
			c.probeMu.Unlock()
			return errors.New("previous storage I/O probe remains blocked")
		}
	}
	checkLeftovers := len(c.pendingProbes) == 0
	c.probeMu.Unlock()
	probeArgs := []string{"internal-probe", "--path", path, "--permission", permission}
	if checkLeftovers && c.backend != nil {
		findCtx, findCancel := context.WithTimeout(ctx, c.cfg.Remora.IOTimeout.Duration)
		processes, err := c.backend.FindProcesses(findCtx, c.executable, probeArgs)
		findCancel()
		if err == nil && len(processes) > 0 {
			return errors.New("previous storage I/O probe remains blocked")
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, c.cfg.Remora.IOTimeout.Duration)
	defer cancel()
	c.probeMu.Lock()
	if previous := c.pendingProbes[key]; previous != nil {
		select {
		case <-previous.done:
			delete(c.pendingProbes, key)
		default:
			c.probeMu.Unlock()
			return errors.New("previous storage I/O probe remains blocked")
		}
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		c.probeMu.Unlock()
		return err
	}
	pending := &pendingProbe{done: make(chan struct{})}
	c.pendingProbes[key] = pending
	c.probeMu.Unlock()
	go func() {
		pending.err = cmd.Wait()
		pending.output = append([]byte(nil), output.Bytes()...)
		close(pending.done)
	}()

	select {
	case <-pending.done:
		c.probeMu.Lock()
		if c.pendingProbes[key] == pending {
			delete(c.pendingProbes, key)
		}
		c.probeMu.Unlock()
		if pending.err == nil {
			return nil
		}
		if message := strings.TrimSpace(string(pending.output)); message != "" {
			return errors.New(strings.TrimPrefix(message, "jellyfin-remora: "))
		}
		return pending.err
	case <-probeCtx.Done():
		if c.backend == nil {
			_ = cmd.Process.Kill()
		} else if err := c.backend.SignalGroup(cmd.Process.Pid, true); err != nil {
			_ = cmd.Process.Kill()
		}
		return errors.New("storage I/O probe timed out")
	}
}

func (c *Checker) expectedSource(ctx context.Context, disk config.DiskConfig) (string, error) {
	if disk.Type == "physical" {
		return c.backend.ResolvePhysical(ctx, disk)
	}
	if disk.Type == "nfs" {
		s := strings.TrimPrefix(disk.Device, "//")
		if !strings.Contains(s, ":") {
			i := strings.IndexByte(s, '/')
			if i > 0 {
				s = s[:i] + ":" + s[i:]
			}
		}
		return s, nil
	}
	s := strings.TrimPrefix(disk.Device, "//")
	if at := strings.LastIndexByte(s, '@'); at >= 0 {
		s = s[at+1:]
	}
	return s, nil
}

func sourceMatches(mi platform.MountInfo, typ, expected string) bool {
	source := strings.TrimPrefix(mi.Source, "//")
	expected = strings.TrimPrefix(expected, "//")
	if typ == "smb" {
		if at := strings.LastIndexByte(source, '@'); at >= 0 {
			source = source[at+1:]
		}
		source, _ = url.PathUnescape(source)
		expected, _ = url.PathUnescape(expected)
		return strings.EqualFold(source, expected) && (mi.FSType == "smbfs" || mi.FSType == "smb" || mi.FSType == "cifs")
	}
	if typ == "nfs" {
		actualHost, actualExport, actualOK := splitNFSSource(source)
		expectedHost, expectedExport, expectedOK := splitNFSSource(expected)
		return actualOK && expectedOK && strings.EqualFold(actualHost, expectedHost) && actualExport == expectedExport && strings.HasPrefix(mi.FSType, "nfs")
	}
	return source == expected
}

func splitNFSSource(source string) (string, string, bool) {
	source = strings.TrimPrefix(strings.TrimSpace(source), "//")
	if strings.HasPrefix(source, "[") {
		end := strings.IndexByte(source, ']')
		if end <= 1 || end+1 >= len(source) {
			return "", "", false
		}
		rest := source[end+1:]
		if strings.HasPrefix(rest, ":") {
			rest = strings.TrimPrefix(rest, ":")
		}
		if !strings.HasPrefix(rest, "/") {
			return "", "", false
		}
		return source[1:end], rest, true
	}
	// The kernel's mount table may omit brackets around an IPv6 literal. The
	// final :/ boundary remains unambiguous because NFS exports are absolute.
	if separator := strings.LastIndex(source, ":/"); separator > 0 {
		return source[:separator], source[separator+1:], true
	}
	if separator := strings.IndexByte(source, '/'); separator > 0 {
		return source[:separator], source[separator:], true
	}
	return "", "", false
}

func smbSourcesEquivalent(ctx context.Context, actual, expected string) bool {
	actualHost, actualShare, ok := splitSMBSource(actual)
	if !ok {
		return false
	}
	expectedHost, expectedShare, ok := splitSMBSource(expected)
	if !ok || !strings.EqualFold(actualShare, expectedShare) {
		return false
	}
	if strings.EqualFold(actualHost, expectedHost) {
		return true
	}
	actualHost = normalizeSMBHostForLookup(actualHost)
	expectedHost = normalizeSMBHostForLookup(expectedHost)
	actualIPs, err := net.DefaultResolver.LookupIPAddr(ctx, actualHost)
	if err != nil {
		return false
	}
	expectedIPs, err := net.DefaultResolver.LookupIPAddr(ctx, expectedHost)
	if err != nil {
		return false
	}
	for _, a := range actualIPs {
		for _, e := range expectedIPs {
			if a.IP.Equal(e.IP) {
				return true
			}
		}
	}
	return false
}
func splitSMBSource(source string) (string, string, bool) {
	source = strings.TrimPrefix(source, "//")
	if at := strings.LastIndexByte(source, '@'); at >= 0 {
		source = source[at+1:]
	}
	parts := strings.SplitN(source, "/", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	share, err := url.PathUnescape(parts[1])
	if err != nil {
		return "", "", false
	}
	return strings.TrimSuffix(parts[0], "."), share, true
}

func normalizeSMBHostForLookup(host string) string {
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	const marker = "._smb._tcp."
	if i := strings.Index(strings.ToLower(host), marker); i >= 0 {
		return host[:i] + "." + host[i+len(marker):]
	}
	return host
}

func findTarget(mounts []platform.MountInfo, target string) (platform.MountInfo, bool) {
	for _, m := range mounts {
		if m.Target == target {
			return m, true
		}
	}
	return platform.MountInfo{}, false
}

func (c *Checker) reachable(ctx context.Context, disk config.DiskConfig) bool {
	if disk.Type == "physical" {
		return true
	}
	host, ok := networkStorageHost(disk)
	if !ok {
		return false
	}
	port := 445
	if disk.Type == "nfs" {
		port = 2049
	}
	d := net.Dialer{Timeout: min(c.cfg.Remora.IOTimeout.Duration, 2*time.Second)}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func networkStorageHost(disk config.DiskConfig) (string, bool) {
	source := strings.TrimPrefix(strings.TrimSpace(disk.Device), "//")
	if at := strings.LastIndexByte(source, '@'); at >= 0 {
		source = source[at+1:]
	}
	if strings.HasPrefix(source, "[") {
		end := strings.IndexByte(source, ']')
		if end <= 1 {
			return "", false
		}
		return source[1:end], true
	}
	separator := strings.IndexByte(source, '/')
	if disk.Type == "nfs" {
		if colon := strings.IndexByte(source, ':'); colon >= 0 && (separator < 0 || colon < separator) {
			separator = colon
		}
	}
	if separator >= 0 {
		source = source[:separator]
	}
	return source, source != ""
}

func redactDevice(d config.DiskConfig) string {
	if d.Password == "" {
		return d.Device
	}
	return redact(d.Device, d.Password)
}
func redact(s, secret string) string {
	if secret == "" {
		return s
	}
	s = strings.ReplaceAll(s, secret, "***")
	s = strings.ReplaceAll(s, url.QueryEscape(secret), "***")
	return strings.ReplaceAll(s, url.PathEscape(secret), "***")
}
