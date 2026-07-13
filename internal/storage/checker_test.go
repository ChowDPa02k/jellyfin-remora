package storage

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
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
	c := &Checker{cfg: &config.Config{Remora: config.RemoraConfig{IOTimeout: config.Duration{Duration: time.Second}}, Jellyfin: config.JellyfinConfig{DataDir: d, ConfigDir: d, CacheDir: d, LogDir: missing}}, executable: exe}
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
