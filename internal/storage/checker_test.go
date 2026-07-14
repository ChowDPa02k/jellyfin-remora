package storage

import (
	"context"
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
	}{{platform.MountInfo{Source: "//user@nas/share", FSType: "smbfs"}, "smb", "nas/share", true}, {platform.MountInfo{Source: "//user@nas/%E5%85%AC%E5%85%B1", FSType: "smbfs"}, "smb", "nas/公共", true}, {platform.MountInfo{Source: "nas:/data", FSType: "nfs"}, "nfs", "nas:/data", true}, {platform.MountInfo{Source: "/dev/disk5s1", FSType: "apfs"}, "physical", "/dev/disk5s1", true}, {platform.MountInfo{Source: "//nas/other", FSType: "smbfs"}, "smb", "nas/share", false}}
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
}

func TestCheckPathsUsesIsolatedProbeProcess(t *testing.T) {
	d := t.TempDir()
	executableName := "remora"
	if runtime.GOOS == "windows" {
		executableName += ".exe"
	}
	exe := filepath.Join(d, executableName)
	cmd := exec.Command("go", "build", "-o", exe, "../../cmd/jellyfin-remora")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build remora: %v: %s", err, b)
	}
	missing := filepath.Join(d, "missing")
	// Starting the helper can take noticeably longer under the race detector on
	// loaded CI runners. Keep this test focused on process isolation rather than
	// making it depend on a one-second process startup deadline.
	c := &Checker{cfg: &config.Config{Remora: config.RemoraConfig{IOTimeout: config.Duration{Duration: 5 * time.Second}}, Jellyfin: config.JellyfinConfig{DataDir: d, ConfigDir: d, CacheDir: d, LogDir: missing}}, executable: exe}
	results := c.CheckPaths(context.Background())
	if len(results) != 2 {
		t.Fatalf("results=%d", len(results))
	}
	if !results[0].Healthy || !results[1].Fatal {
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
