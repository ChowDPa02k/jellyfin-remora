//go:build windows

package platform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/probe"
)

var procWNetCancelConnection2WTest = mpr.NewProc("WNetCancelConnection2W")

func TestWindowsLiveConfiguredStorage(t *testing.T) {
	if os.Getenv("REMORA_WINDOWS_LIVE_STORAGE") != "1" {
		t.Skip("set REMORA_WINDOWS_LIVE_STORAGE=1 to exercise config.yaml storage")
	}
	cfg, err := config.Load(filepath.Join("..", "..", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	backend := newBackend()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for _, disk := range cfg.Disks {
		disk := disk
		t.Run(disk.Type+"-"+strings.TrimSuffix(filepath.Base(filepath.Clean(disk.Target)), "\\"), func(t *testing.T) {
			expected := disk.Device
			if disk.Type == "physical" {
				expected, err = backend.ResolvePhysical(ctx, disk)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := backend.Mount(ctx, disk); err != nil {
				t.Fatalf("make configured storage available: %v", err)
			}
			mounts, err := backend.Mounts(ctx)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, mount := range mounts {
				if strings.EqualFold(filepath.Clean(mount.Target), filepath.Clean(disk.Target)) {
					found = true
					if disk.Type == "physical" && !strings.EqualFold(canonicalVolumeGUID(mount.Source), canonicalVolumeGUID(expected)) {
						t.Fatalf("physical source = %s, want %s", mount.Source, expected)
					}
					if disk.Type == "smb" {
						actual := strings.TrimPrefix(strings.ReplaceAll(mount.Source, `\`, "/"), "//")
						wanted := strings.TrimPrefix(strings.ReplaceAll(disk.Device, `\`, "/"), "//")
						if !strings.EqualFold(actual, wanted) {
							t.Fatalf("SMB source = %s, want %s", mount.Source, disk.Device)
						}
					}
				}
			}
			if !found {
				t.Fatalf("target %s absent after mount/connect", disk.Target)
			}
			probe := disk.ProbePath
			if probe == "" {
				probe = disk.Target
			}
			if _, err := os.Stat(probe); err != nil {
				t.Fatalf("probe path %s: %v", probe, err)
			}
		})
	}
}

func TestWindowsDiscoverConfiguredPhysicalVolume(t *testing.T) {
	if os.Getenv("REMORA_WINDOWS_LIVE_STORAGE") != "1" {
		t.Skip("set REMORA_WINDOWS_LIVE_STORAGE=1 to exercise config.yaml storage")
	}
	cfg, err := config.Load(filepath.Join("..", "..", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	volumes, err := DiscoverVolumes()
	if err != nil {
		t.Fatal(err)
	}
	for _, disk := range cfg.Disks {
		if disk.Type != "physical" {
			continue
		}
		for _, volume := range volumes {
			if !strings.EqualFold(canonicalVolumeGUID(volume.GUID), canonicalVolumeGUID(disk.VolumeGUID)) {
				continue
			}
			if !strings.EqualFold(volume.Label, disk.VolumeLabel) || !strings.EqualFold(volume.Filesystem, disk.Filesystem) {
				t.Fatalf("discovered identity = label %q filesystem %q", volume.Label, volume.Filesystem)
			}
			for _, path := range volume.Paths {
				if strings.EqualFold(filepath.Clean(path), filepath.Clean(disk.Target)) {
					return
				}
			}
			t.Fatalf("configured target %s absent from discovered paths %v", disk.Target, volume.Paths)
		}
		t.Fatalf("configured volume %s was not discovered", disk.VolumeGUID)
	}
	t.Fatal("config.yaml has no physical disk")
}

func TestWindowsLivePhysicalIdentityFailures(t *testing.T) {
	if os.Getenv("REMORA_WINDOWS_LIVE_STORAGE") != "1" {
		t.Skip("set REMORA_WINDOWS_LIVE_STORAGE=1 to exercise config.yaml storage")
	}
	cfg, err := config.Load(filepath.Join("..", "..", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var configured config.DiskConfig
	for _, disk := range cfg.Disks {
		if disk.Type == "physical" {
			configured = disk
			break
		}
	}
	if configured.VolumeGUID == "" {
		t.Fatal("config.yaml has no physical volume")
	}
	backend := newBackend()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Run("wrong-label", func(t *testing.T) {
		disk := configured
		disk.VolumeLabel += "-wrong"
		if _, err := backend.ResolvePhysical(ctx, disk); err == nil || !strings.Contains(err.Error(), "label mismatch") {
			t.Fatalf("ResolvePhysical error = %v", err)
		}
	})
	t.Run("wrong-filesystem", func(t *testing.T) {
		disk := configured
		disk.Filesystem = "ReFS"
		if _, err := backend.ResolvePhysical(ctx, disk); err == nil || !strings.Contains(err.Error(), "filesystem mismatch") {
			t.Fatalf("ResolvePhysical error = %v", err)
		}
	})
	t.Run("missing-target", func(t *testing.T) {
		disk := configured
		disk.Target = unusedWindowsDriveRoot(t)
		if err := backend.Mount(ctx, disk); err == nil || !strings.Contains(err.Error(), "not mounted") {
			t.Fatalf("Mount error = %v", err)
		}
	})
	t.Run("reused-drive-letter", func(t *testing.T) {
		volumes, err := DiscoverVolumes()
		if err != nil {
			t.Fatal(err)
		}
		for _, volume := range volumes {
			if strings.EqualFold(canonicalVolumeGUID(volume.GUID), canonicalVolumeGUID(configured.VolumeGUID)) || len(volume.Paths) == 0 {
				continue
			}
			disk := configured
			disk.Target = volume.Paths[0]
			if err := backend.Mount(ctx, disk); err == nil || !strings.Contains(err.Error(), "want") {
				t.Fatalf("Mount error = %v", err)
			}
			return
		}
		t.Skip("host has no second mounted local volume")
	})
}

func TestWindowsLiveSMBDisconnectReconnect(t *testing.T) {
	if os.Getenv("REMORA_WINDOWS_LIVE_STORAGE") != "1" {
		t.Skip("set REMORA_WINDOWS_LIVE_STORAGE=1 to exercise config.yaml storage")
	}
	cfg, err := config.Load(filepath.Join("..", "..", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var disk config.DiskConfig
	for _, candidate := range cfg.Disks {
		if candidate.Type == "smb" {
			disk = candidate
			break
		}
	}
	if disk.Device == "" {
		t.Skip("config.yaml has no SMB disk")
	}

	disk.Target = unusedWindowsDriveRoot(t)
	disk.ProbePath = ""
	backend := newBackend()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	t.Cleanup(func() { _ = cancelWindowsNetworkDrive(disk.Target) })

	if err := backend.Mount(ctx, disk); err != nil {
		t.Fatalf("initial temporary SMB connection: %v", err)
	}
	assertWindowsSMBSource(t, disk.Target, disk.Device)
	probeWindowsSMBWrite(t, disk.Target)

	if err := cancelWindowsNetworkDrive(disk.Target); err != nil {
		t.Fatalf("disconnect temporary SMB mapping: %v", err)
	}
	if _, err := networkSource(disk.Target); err == nil {
		t.Fatalf("temporary SMB mapping %s remained connected", disk.Target)
	}
	if err := backend.Mount(ctx, disk); err != nil {
		t.Fatalf("reconnect temporary SMB mapping: %v", err)
	}
	assertWindowsSMBSource(t, disk.Target, disk.Device)
	probeWindowsSMBWrite(t, disk.Target)
}

func TestWindowsLiveNFSUnreachableFailsWithinDeadline(t *testing.T) {
	if os.Getenv("REMORA_WINDOWS_LIVE_STORAGE") != "1" {
		t.Skip("set REMORA_WINDOWS_LIVE_STORAGE=1 to exercise the system Windows NFS client")
	}
	if _, err := windowsSystemMountExecutable(); err != nil {
		t.Skipf("Windows Client for NFS is unavailable: %v", err)
	}
	disk := config.DiskConfig{
		Type:       "nfs",
		Device:     "nonexistent.invalid:/remora-missing-export",
		Target:     unusedWindowsDriveRoot(t),
		Options:    "mtype=hard,timeout=1,retry=1",
		Permission: "r",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := time.Now()
	err := newBackend().Mount(ctx, disk)
	if err == nil {
		t.Fatalf("unreachable NFS export unexpectedly mounted at %s", disk.Target)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("unreachable NFS mount exceeded bounded deadline: %s", elapsed)
	}
	if _, err := networkSource(disk.Target); err == nil {
		t.Fatalf("failed NFS mount left target %s connected", disk.Target)
	}
}

func TestWindowsLiveNFSMountDisconnectReconnect(t *testing.T) {
	source := os.Getenv("REMORA_WINDOWS_LIVE_NFS_SOURCE")
	if os.Getenv("REMORA_WINDOWS_LIVE_STORAGE") != "1" || source == "" {
		t.Skip("set REMORA_WINDOWS_LIVE_STORAGE=1 and REMORA_WINDOWS_LIVE_NFS_SOURCE to exercise a live NFS export")
	}
	if _, err := windowsSystemMountExecutable(); err != nil {
		t.Skipf("Windows Client for NFS is unavailable: %v", err)
	}
	disk := config.DiskConfig{
		Type: "nfs", Device: source, Target: unusedWindowsDriveRoot(t),
		Options: "anon,mtype=soft,timeout=2,retry=1", Permission: "r",
	}
	backend := newBackend()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	t.Cleanup(func() { _ = unmountWindowsNFS(disk.Target) })

	for attempt := 1; attempt <= 2; attempt++ {
		if err := backend.Mount(ctx, disk); err != nil {
			t.Fatalf("NFS mount attempt %d: %v", attempt, err)
		}
		mounts, err := backend.Mounts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		mount, ok := findMountTarget(mounts, disk.Target)
		if !ok || mount.FSType != "nfs" || normalizeWindowsNFSSource(mount.Source) != normalizeWindowsNFSSource(source) {
			t.Fatalf("NFS mount attempt %d = %#v", attempt, mount)
		}
		if err := probe.Path(disk.Target, "r"); err != nil {
			t.Fatalf("NFS read probe attempt %d: %v", attempt, err)
		}
		if err := unmountWindowsNFS(disk.Target); err != nil {
			t.Fatalf("NFS unmount attempt %d: %v", attempt, err)
		}
	}
}

func unmountWindowsNFS(target string) error {
	command := exec.Command("umount.exe", "-f", strings.TrimSuffix(target, `\`))
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("unmount Windows NFS %s: %s: %w", target, strings.TrimSpace(string(output)), err)
	}
	return nil
}

func findMountTarget(mounts []MountInfo, target string) (MountInfo, bool) {
	for _, mount := range mounts {
		if strings.EqualFold(mount.Target, target) {
			return mount, true
		}
	}
	return MountInfo{}, false
}

func cancelWindowsNetworkDrive(target string) error {
	local, err := syscall.UTF16PtrFromString(strings.TrimSuffix(target, `\`))
	if err != nil {
		return err
	}
	r1, _, _ := procWNetCancelConnection2WTest.Call(uintptr(unsafe.Pointer(local)), 0, 1)
	if r1 != 0 {
		return syscall.Errno(r1)
	}
	return nil
}

func assertWindowsSMBSource(t *testing.T, target, expected string) {
	t.Helper()
	actual, err := networkSource(target)
	if err != nil {
		t.Fatal(err)
	}
	if !sameSMBSource(actual, expected) {
		t.Fatalf("SMB source = %s, want %s", actual, expected)
	}
}

func probeWindowsSMBWrite(t *testing.T, target string) {
	t.Helper()
	probe, err := os.CreateTemp(target, ".remora-smb-integration-*")
	if err != nil {
		t.Fatal(err)
	}
	name := probe.Name()
	defer os.Remove(name)
	if _, err := probe.Write([]byte("remora SMB probe\n")); err != nil {
		probe.Close()
		t.Fatal(err)
	}
	if err := probe.Sync(); err != nil {
		probe.Close()
		t.Fatal(err)
	}
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
}

func unusedWindowsDriveRoot(t *testing.T) string {
	t.Helper()
	drives, err := logicalDrives()
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, drive := range drives {
		used[strings.ToUpper(drive[:1])] = true
	}
	for letter := 'Z'; letter >= 'D'; letter-- {
		if !used[string(letter)] {
			return string(letter) + `:\`
		}
	}
	t.Fatal("no unused drive letter available")
	return ""
}
