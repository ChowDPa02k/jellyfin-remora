package storage

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
)

func TestSourceMatches(t *testing.T) {
	tests := []struct {
		m         platform.MountInfo
		typ, want string
		ok        bool
	}{{platform.MountInfo{Source: "//user@nas/share", FSType: "smbfs"}, "smb", "nas/share", true}, {platform.MountInfo{Source: "//user@nas/%E5%85%AC%E5%85%B1", FSType: "smbfs"}, "smb", "nas/公共", true}, {platform.MountInfo{Source: "nas:/data", FSType: "nfs"}, "nfs", "nas:/data", true}, {platform.MountInfo{Source: "2001:db8::30:/data", FSType: "nfs4"}, "nfs", "[2001:db8::30]:/data", true}, {platform.MountInfo{Source: "/dev/disk5s1", FSType: "apfs"}, "physical", "/dev/disk5s1", true}, {platform.MountInfo{Source: "//nas/other", FSType: "smbfs"}, "smb", "nas/share", false}}
	for _, tt := range tests {
		if got := sourceMatches(tt.m, tt.typ, tt.want); got != tt.ok {
			t.Errorf("sourceMatches(%+v,%s,%s)=%t", tt.m, tt.typ, tt.want, got)
		}
	}
}

func TestSplitSMBSource(t *testing.T) {
	host, share, ok := splitSMBSource("//user@NAS.local/nas_%E5%85%AC%E5%85%B1%E7%A9%BA%E9%97%B4")
	if !ok || host != "NAS.local" || share != "nas_公共空间" {
		t.Fatalf("host=%q share=%q ok=%t", host, share, ok)
	}
}

func TestNormalizeBonjourSMBServiceHost(t *testing.T) {
	if got := normalizeSMBHostForLookup("UGREEN-CD13._smb._tcp.local"); got != "UGREEN-CD13.local" {
		t.Fatalf("host=%q", got)
	}
	if got := normalizeSMBHostForLookup("[2001:db8::20]"); got != "2001:db8::20" {
		t.Fatalf("IPv6 host=%q", got)
	}
}

func TestNetworkStorageHost(t *testing.T) {
	tests := []struct {
		name string
		disk config.DiskConfig
		want string
		ok   bool
	}{
		{name: "SMB DNS", disk: config.DiskConfig{Type: "smb", Device: "//user@nas.local/share"}, want: "nas.local", ok: true},
		{name: "SMB IPv6", disk: config.DiskConfig{Type: "smb", Device: "//[2001:db8::20]/share"}, want: "2001:db8::20", ok: true},
		{name: "NFS conventional", disk: config.DiskConfig{Type: "nfs", Device: "nas.local:/exports/media"}, want: "nas.local", ok: true},
		{name: "NFS legacy slash", disk: config.DiskConfig{Type: "nfs", Device: "nas.local/exports/media"}, want: "nas.local", ok: true},
		{name: "NFS IPv6", disk: config.DiskConfig{Type: "nfs", Device: "[2001:db8::30]:/exports/media"}, want: "2001:db8::30", ok: true},
		{name: "empty", disk: config.DiskConfig{Type: "nfs"}, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := networkStorageHost(tt.disk)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("networkStorageHost() = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestSplitNFSSource(t *testing.T) {
	tests := []struct {
		source       string
		host, export string
		ok           bool
	}{
		{source: "nas.local:/exports/media", host: "nas.local", export: "/exports/media", ok: true},
		{source: "nas.local/exports/media", host: "nas.local", export: "/exports/media", ok: true},
		{source: "[2001:db8::30]:/exports/media", host: "2001:db8::30", export: "/exports/media", ok: true},
		{source: "2001:db8::30:/exports/media", host: "2001:db8::30", export: "/exports/media", ok: true},
		{source: "missing-export", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			host, export, ok := splitNFSSource(tt.source)
			if host != tt.host || export != tt.export || ok != tt.ok {
				t.Fatalf("splitNFSSource() = %q, %q, %v; want %q, %q, %v", host, export, ok, tt.host, tt.export, tt.ok)
			}
		})
	}
}

func TestCheckPathsUsesIsolatedProbeProcess(t *testing.T) {
	d := t.TempDir()
	executableName := "remora"
	if runtime.GOOS == "windows" {
		executableName += ".exe"
	}
	exe := filepath.Join(d, executableName)
	if prepared := os.Getenv("JELLYFIN_REMORA_TEST_BINARY"); prepared != "" {
		data, err := os.ReadFile(prepared)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(exe, data, 0o755); err != nil {
			t.Fatal(err)
		}
	} else {
		cmd := exec.Command("go", "build", "-o", exe, "../../cmd/jellyfin-remora")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build remora: %v: %s", err, b)
		}
	}
	missing := filepath.Join(d, "missing")
	transcode := filepath.Join(d, "transcode")
	if err := os.Mkdir(transcode, 0o750); err != nil {
		t.Fatal(err)
	}
	// Starting the helper can take noticeably longer under the race detector on
	// loaded CI runners. Keep this test focused on process isolation rather than
	// making it depend on a one-second process startup deadline.
	c := &Checker{cfg: &config.Config{Remora: config.RemoraConfig{IOTimeout: config.Duration{Duration: 5 * time.Second}}, Jellyfin: config.JellyfinConfig{DataDir: d, ConfigDir: d, CacheDir: d, LogDir: missing, Playback: config.PlaybackConfig{Transcoding: config.TranscodingConfig{TranscodePath: config.Optional[string]{Set: true, Value: transcode}}}}}, executable: exe}
	results := c.CheckPaths(context.Background())
	if len(results) != 3 {
		t.Fatalf("results=%d", len(results))
	}
	if !results[0].Healthy || !results[1].Fatal || !results[2].Healthy {
		t.Fatalf("results=%+v", results)
	}
}
func TestRedactEscapedSecret(t *testing.T) {
	got := redact("mount //u:p%40ss@nas/share", "p@ss")
	if got != "mount //u:***@nas/share" {
		t.Fatalf("redacted=%q", got)
	}
}

func TestDiskFailureThresholdRequiresConsecutiveFailuresAfterHealthyCheck(t *testing.T) {
	checker := &Checker{}
	disk := config.DiskConfig{FailureThreshold: 3}
	failure := model.StorageResult{Fatal: true, Message: "I/O failed"}
	if got := checker.applyFailureThreshold(0, disk, failure); !got.Fatal {
		t.Fatal("initial preflight failure must remain fatal")
	}
	if got := checker.applyFailureThreshold(0, disk, model.StorageResult{Healthy: true}); !got.Healthy {
		t.Fatal("healthy baseline was not retained")
	}
	for attempt := 1; attempt <= 3; attempt++ {
		got := checker.applyFailureThreshold(0, disk, failure)
		if got.Fatal != (attempt == 3) {
			t.Fatalf("attempt %d fatal=%t message=%q", attempt, got.Fatal, got.Message)
		}
	}
	checker.applyFailureThreshold(0, disk, model.StorageResult{Healthy: true})
	if got := checker.applyFailureThreshold(0, disk, failure); got.Fatal || !strings.Contains(got.Message, "failure 1/3") {
		t.Fatalf("success did not reset threshold: %+v", got)
	}
}

func TestInitMismatchAllowanceDoesNotChangeRuntimeCheck(t *testing.T) {
	cfg := &config.Config{
		Remora: config.RemoraConfig{IOTimeout: config.Duration{Duration: time.Second}},
		Disks: []config.DiskConfig{{
			Type:       "physical",
			Device:     "/dev/expected",
			Target:     "/Volumes/AppData",
			Permission: "rw",
		}},
	}
	backend := &mismatchBackend{mounts: []platform.MountInfo{{Source: "/dev/actual", Target: "/Volumes/AppData", FSType: "apfs"}}}
	checker := &Checker{
		cfg:           cfg,
		backend:       backend,
		failureCounts: make([]int, 1), confirmedHealthy: make([]bool, 1),
		probeOverride: func(context.Context, string, string) error { return nil },
	}
	if got := checker.CheckDisk(context.Background(), 0); !got.Fatal || !strings.Contains(got.Message, "mount source mismatch") {
		t.Fatalf("runtime check = %+v", got)
	}
	if got := checker.CheckDiskForInit(context.Background(), 0, true); !got.Healthy || got.Fatal || !strings.Contains(got.Message, "mount source mismatch") {
		t.Fatalf("accepted init check = %+v", got)
	}
}

func TestRuntimeMountLossFencesBeforeRecoveryMount(t *testing.T) {
	cfg := &config.Config{
		Remora: config.RemoraConfig{IOTimeout: config.Duration{Duration: time.Second}},
		Disks:  []config.DiskConfig{{Type: "physical", Device: "/dev/expected", Target: "/srv/appdata", Permission: "rw", FailureThreshold: 1}},
	}
	backend := &mismatchBackend{mountSource: "/dev/expected"}
	checker := &Checker{
		cfg: cfg, backend: backend, recoveryMounts: true,
		failureCounts: make([]int, 1), confirmedHealthy: make([]bool, 1),
		probeOverride: func(context.Context, string, string) error { return nil },
	}
	if got := checker.CheckDisk(context.Background(), 0); !got.Healthy || backend.mountCalls != 1 {
		t.Fatalf("startup mount = %+v calls=%d", got, backend.mountCalls)
	}
	checker.SetMountRecoveryAllowed(false)
	backend.mounts = nil
	if got := checker.CheckDisk(context.Background(), 0); !got.Fatal || backend.mountCalls != 1 {
		t.Fatalf("runtime loss remounted before fencing: %+v calls=%d", got, backend.mountCalls)
	}
	checker.SetMountRecoveryAllowed(true)
	if got := checker.CheckDisk(context.Background(), 0); !got.Healthy || backend.mountCalls != 2 {
		t.Fatalf("fenced recovery did not mount: %+v calls=%d", got, backend.mountCalls)
	}
}

func TestRuntimeCheckerUsesJellyfinIdentity(t *testing.T) {
	cfg := &config.Config{Jellyfin: config.JellyfinConfig{RunAsUser: "jellyfin", RunAsGroup: "media"}}
	checker, err := New(cfg, &mismatchBackend{})
	if err != nil {
		t.Fatal(err)
	}
	if checker.probeUsername != "jellyfin" || checker.probeGroup != "media" {
		t.Fatalf("probe identity = %q:%q", checker.probeUsername, checker.probeGroup)
	}
}

func TestProbeTimeoutReturnsWithoutStackingBlockedProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell")
	}
	directory := t.TempDir()
	helper := filepath.Join(directory, "blocked-probe")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	backend := &mismatchBackend{leaveSignaledProcess: true}
	checker := &Checker{
		cfg:     &config.Config{Remora: config.RemoraConfig{IOTimeout: config.Duration{Duration: 50 * time.Millisecond}}},
		backend: backend, executable: helper,
	}
	if err := checker.probePath(context.Background(), directory, "r"); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("first probe error = %v", err)
	}
	if backend.signaledPID <= 0 {
		t.Fatal("timed-out probe was not signaled")
	}
	t.Cleanup(func() {
		if process, err := os.FindProcess(backend.signaledPID); err == nil {
			_ = process.Kill()
		}
	})
	started := time.Now()
	if err := checker.probePath(context.Background(), directory, "r"); err == nil || !strings.Contains(err.Error(), "remains blocked") {
		t.Fatalf("second probe error = %v", err)
	}
	if time.Since(started) > 200*time.Millisecond {
		t.Fatalf("blocked-probe suppression took %s", time.Since(started))
	}
}

func TestLeftoverDiscoveryDoesNotConsumeIOProbeDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell")
	}
	directory := t.TempDir()
	helper := filepath.Join(directory, "probe")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nsleep 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	checker := &Checker{
		cfg:           &config.Config{Remora: config.RemoraConfig{IOTimeout: config.Duration{Duration: 2 * time.Second}}},
		backend:       &mismatchBackend{findDelay: 1500 * time.Millisecond},
		executable:    helper,
		pendingProbes: make(map[string]*pendingProbe),
	}
	if err := checker.probePath(context.Background(), directory, "r"); err != nil {
		t.Fatalf("probe inherited leftover-discovery latency: %v", err)
	}
}

type mismatchBackend struct {
	mounts               []platform.MountInfo
	mountSource          string
	mountCalls           int
	leaveSignaledProcess bool
	signaledPID          int
	findDelay            time.Duration
}

func (b *mismatchBackend) Mounts(context.Context) ([]platform.MountInfo, error) {
	return append([]platform.MountInfo(nil), b.mounts...), nil
}
func (b *mismatchBackend) Mount(_ context.Context, disk config.DiskConfig) error {
	b.mountCalls++
	if b.mountSource != "" {
		b.mounts = []platform.MountInfo{{Source: b.mountSource, Target: disk.Target, FSType: "ext4"}}
	}
	return nil
}
func (*mismatchBackend) ResolvePhysical(context.Context, config.DiskConfig) (string, error) {
	return "/dev/expected", nil
}
func (*mismatchBackend) ExecutableProvenance(string) (bool, error)        { return false, nil }
func (*mismatchBackend) ConfigureProcess(*exec.Cmd, string, string) error { return nil }
func (b *mismatchBackend) SignalGroup(pid int, _ bool) error {
	b.signaledPID = pid
	if b.leaveSignaledProcess {
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
func (*mismatchBackend) ProcessInfo(context.Context, int) (platform.ProcessInfo, error) {
	return platform.ProcessInfo{}, nil
}
func (b *mismatchBackend) FindProcesses(context.Context, string, []string) ([]platform.ProcessInfo, error) {
	time.Sleep(b.findDelay)
	return nil, nil
}
