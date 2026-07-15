package storage

import (
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
	failureMu        sync.Mutex
	failureCounts    []int
	confirmedHealthy []bool
	probeOverride    func(context.Context, string, string) error
}

func New(cfg *config.Config, backend platform.Backend) (*Checker, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return NewWithExecutable(cfg, backend, exe)
}

func NewWithExecutable(cfg *config.Config, backend platform.Backend, executable string) (*Checker, error) {
	if executable == "" {
		return nil, errors.New("storage probe executable is required")
	}
	return &Checker{cfg: cfg, backend: backend, executable: executable, failureCounts: make([]int, len(cfg.Disks)), confirmedHealthy: make([]bool, len(cfg.Disks))}, nil
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
	return c.applyFailureThreshold(index, disk, c.checkRaw(ctx, index, disk, true, false))
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
	if !sourceMatches(mi, disk.Type, expected) && !(disk.Type == "smb" && smbSourcesEquivalent(ctx, mi.Source, expected)) {
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
	probeCtx, cancel := context.WithTimeout(ctx, c.cfg.Remora.IOTimeout.Duration)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, c.executable, "internal-probe", "--path", path, "--permission", permission)
	b, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if probeCtx.Err() != nil {
		return errors.New("storage I/O probe timed out")
	}
	if message := strings.TrimSpace(string(b)); message != "" {
		return errors.New(strings.TrimPrefix(message, "jellyfin-remora: "))
	}
	return err
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
		return strings.EqualFold(source, expected) && (mi.FSType == "smbfs" || mi.FSType == "smb")
	}
	if typ == "nfs" {
		return source == expected && strings.HasPrefix(mi.FSType, "nfs")
	}
	return source == expected
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
	host := strings.TrimPrefix(disk.Device, "//")
	if at := strings.LastIndexByte(host, '@'); at >= 0 {
		host = host[at+1:]
	}
	if i := strings.IndexAny(host, "/:"); i >= 0 {
		host = host[:i]
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
