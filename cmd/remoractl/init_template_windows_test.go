//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
	"gopkg.in/yaml.v3"
)

func TestPrepareWindowsPhysicalProbePathAfterIdentityCheck(t *testing.T) {
	target := t.TempDir()
	guid, err := platform.VolumeGUIDForPath(target)
	if err != nil {
		t.Fatal(err)
	}
	probePath := filepath.Join(target, "new-data-root")
	cfg := &config.Config{Disks: []config.DiskConfig{{
		Type: "physical", Target: target, ProbePath: probePath, VolumeGUID: guid,
	}}}
	prepared, err := preparePlatformInitProbePath(cfg, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if !prepared {
		t.Fatal("probe path was not prepared")
	}
	if info, err := os.Stat(probePath); err != nil || !info.IsDir() {
		t.Fatalf("prepared probe path: info=%v err=%v", info, err)
	}
}

func TestPrepareWindowsTemplateFromDriveLetter(t *testing.T) {
	oldDiscover := discoverWindowsVolumes
	discoverWindowsVolumes = func() ([]platform.VolumeInfo, error) {
		return []platform.VolumeInfo{{
			GUID:  `\\?\Volume{8d63858a-6ae4-44bf-b0ed-c3a00f467af4}\`,
			Paths: []string{`D:\`}, Label: "STORAGE", Filesystem: "NTFS",
			TotalBytes: 1024208142336, FreeBytes: 645210000000,
		}}, nil
	}
	t.Cleanup(func() { discoverWindowsVolumes = oldDiscover })

	template := []byte(`# keep this comment
config-version: 2
disk:
  - type: physical
    volume-guid: '\\?\Volume{00000000-0000-0000-0000-000000000000}\'
    target: 'D:\'
    probe-path: 'D:\jellyfin'
jellyfin:
  data-dir: 'D:\jellyfin\data'
  config-dir: 'D:\jellyfin\config'
  cache-dir: 'D:\jellyfin\cache'
  log-dir: 'D:\jellyfin\log'
`)
	prepared, err := preparePlatformTemplate(template, `d:\`, `D:\media\jellyfin`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prepared), "# keep this comment") {
		t.Fatal("template comment was not preserved")
	}
	var document yaml.Node
	if err := yaml.Unmarshal(prepared, &document); err != nil {
		t.Fatal(err)
	}
	disk := firstPhysicalDisk(&document)
	for key, want := range map[string]string{
		"volume-guid":  `\\?\Volume{8d63858a-6ae4-44bf-b0ed-c3a00f467af4}\`,
		"volume-label": "STORAGE", "filesystem": "NTFS", "target": `D:\`, "probe-path": `D:\media\jellyfin`,
	} {
		if got := windowsMappingValue(disk, key); got == nil || got.Value != want {
			t.Fatalf("disk.%s = %#v, want %q", key, got, want)
		}
	}
}

func TestPrepareWindowsTemplateRejectsUnknownMountPoint(t *testing.T) {
	oldDiscover := discoverWindowsVolumes
	discoverWindowsVolumes = func() ([]platform.VolumeInfo, error) {
		return []platform.VolumeInfo{{GUID: `\\?\Volume{8d63858a-6ae4-44bf-b0ed-c3a00f467af4}\`, Paths: []string{`D:\`}}}, nil
	}
	t.Cleanup(func() { discoverWindowsVolumes = oldDiscover })
	_, err := preparePlatformTemplate([]byte("disk:\n  - type: physical\n    volume-guid: '\\\\?\\Volume{00000000-0000-0000-0000-000000000000}\\'\n"), `E:\`, "")
	if err == nil || !strings.Contains(err.Error(), "was not discovered") {
		t.Fatalf("error = %v", err)
	}
}

func TestWindowsDataRootMustBeBeneathSelectedVolume(t *testing.T) {
	if _, err := windowsDataRoot(`D:\`, `C:\jellyfin`); err == nil || !strings.Contains(err.Error(), "beneath") {
		t.Fatalf("windowsDataRoot error = %v", err)
	}
	if _, err := windowsDataRoot(`D:\`, `D:\`); err == nil || !strings.Contains(err.Error(), "beneath") {
		t.Fatalf("windowsDataRoot root error = %v", err)
	}
}

func TestVerifyWindowsStorageBoundaryRejectsJunctionToAnotherVolume(t *testing.T) {
	root := t.TempDir()
	rootGUID, err := platform.VolumeGUIDForPath(root)
	if err != nil {
		t.Fatal(err)
	}
	volumes, err := platform.DiscoverVolumes()
	if err != nil {
		t.Fatal(err)
	}
	var otherPath string
	for _, volume := range volumes {
		if strings.EqualFold(strings.TrimSuffix(volume.GUID, `\`), strings.TrimSuffix(rootGUID, `\`)) || len(volume.Paths) == 0 {
			continue
		}
		otherPath = volume.Paths[0]
		break
	}
	if otherPath == "" {
		t.Skip("host has no second mounted local volume")
	}

	junction := filepath.Join(root, "escape")
	command := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", junction, otherPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("cannot create junction fixture: %v: %s", err, output)
	}
	t.Cleanup(func() { _ = os.Remove(junction) })
	disk := config.DiskConfig{Type: "physical", Target: filepath.VolumeName(root) + `\`, VolumeGUID: rootGUID}
	err = verifyWindowsStorageBoundary(filepath.Join(junction, "jellyfin", "data"), disk, false)
	if err == nil || (!strings.Contains(err.Error(), "outside configured target") && !strings.Contains(err.Error(), "resolves to volume")) {
		t.Fatalf("verifyWindowsStorageBoundary error = %v", err)
	}
}
