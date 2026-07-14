//go:build windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
	"gopkg.in/yaml.v3"
)

var discoverWindowsVolumes = platform.DiscoverVolumes

func preparePlatformInitDirectories(cfg *config.Config) error {
	if len(cfg.Disks) == 0 {
		return nil
	}
	backend := platform.New()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Remora.IOTimeout.Duration*time.Duration(max(1, len(cfg.Disks))))
	defer cancel()
	for _, disk := range cfg.Disks {
		if err := backend.Mount(ctx, disk); err != nil {
			return fmt.Errorf("verify disk target %s: %w", disk.Target, err)
		}
	}
	for _, path := range []string{cfg.Jellyfin.DataDir, cfg.Jellyfin.ConfigDir, cfg.Jellyfin.CacheDir, cfg.Jellyfin.LogDir} {
		disk, ok := configuredWindowsStorageForPath(path, cfg.Disks)
		if !ok {
			return fmt.Errorf("refusing to create %s outside configured storage", path)
		}
		if err := verifyWindowsStorageBoundary(path, disk); err != nil {
			return err
		}
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(path, 0o750); err != nil {
			return fmt.Errorf("create %s: %w", path, err)
		}
		if err := verifyWindowsStorageBoundary(path, disk); err != nil {
			return err
		}
	}
	return nil
}

func pathIsOnConfiguredWindowsStorage(path string, disks []config.DiskConfig) bool {
	_, ok := configuredWindowsStorageForPath(path, disks)
	return ok
}

func configuredWindowsStorageForPath(path string, disks []config.DiskConfig) (config.DiskConfig, bool) {
	path = strings.ToLower(filepath.Clean(path))
	for _, disk := range disks {
		root := strings.ToLower(filepath.Clean(disk.Target))
		relative, err := filepath.Rel(root, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, `..\`) && relative != "." {
			return disk, true
		}
	}
	return config.DiskConfig{}, false
}

func verifyWindowsStorageBoundary(path string, disk config.DiskConfig) error {
	ancestor, err := nearestExistingWindowsAncestor(path)
	if err != nil {
		return fmt.Errorf("inspect storage boundary for %s: %w", path, err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(disk.Target)
	if err != nil {
		return fmt.Errorf("resolve configured storage target %s: %w", disk.Target, err)
	}
	resolvedAncestor, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return fmt.Errorf("resolve storage path ancestor %s: %w", ancestor, err)
	}
	relative, err := filepath.Rel(resolvedTarget, resolvedAncestor)
	if err != nil || relative == ".." || strings.HasPrefix(relative, `..\`) {
		return fmt.Errorf("storage path %s resolves outside configured target %s", path, disk.Target)
	}
	if disk.Type == "physical" {
		actual, err := platform.VolumeGUIDForPath(resolvedAncestor)
		if err != nil {
			return err
		}
		canonical := func(value string) string {
			return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), `\`))
		}
		if canonical(actual) != canonical(disk.VolumeGUID) {
			return fmt.Errorf("storage path %s resolves to volume %s, want %s", path, actual, disk.VolumeGUID)
		}
	}
	return nil
}

func nearestExistingWindowsAncestor(path string) (string, error) {
	candidate := filepath.Clean(path)
	for {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return "", fmt.Errorf("no existing ancestor for %s", path)
		}
		candidate = parent
	}
}

func preparePlatformTemplate(template []byte, requestedVolume, requestedDataRoot string) ([]byte, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(template, &document); err != nil {
		return nil, fmt.Errorf("decode Windows configuration template: %w", err)
	}
	physical := firstPhysicalDisk(&document)
	if physical == nil {
		if requestedVolume != "" || requestedDataRoot != "" {
			return nil, fmt.Errorf("--volume/--data-root was provided but the template has no physical disk entry")
		}
		return template, nil
	}
	guid := windowsMappingValue(physical, "volume-guid")
	if requestedVolume == "" && requestedDataRoot == "" && guid != nil && !strings.Contains(guid.Value, "00000000-0000-0000-0000-000000000000") {
		return template, nil
	}
	if requestedVolume == "" {
		if target := windowsMappingValue(physical, "target"); target != nil {
			requestedVolume = target.Value
		}
	}

	volumes, err := discoverWindowsVolumes()
	if err != nil {
		return nil, fmt.Errorf("discover Windows volumes: %w", err)
	}
	if len(volumes) == 0 {
		return nil, fmt.Errorf("no local fixed or removable Windows volumes were discovered")
	}
	selected, target, err := selectWindowsVolume(volumes, requestedVolume)
	if err != nil {
		return nil, err
	}
	base, err := windowsDataRoot(target, requestedDataRoot)
	if err != nil {
		return nil, err
	}
	applyWindowsVolume(&document, physical, selected, target, base)

	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return nil, fmt.Errorf("encode Windows configuration template: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func selectWindowsVolume(volumes []platform.VolumeInfo, requested string) (platform.VolumeInfo, string, error) {
	if requested != "" {
		requested = windowsCleanMountPath(requested)
		for _, volume := range volumes {
			for _, path := range volume.Paths {
				if strings.EqualFold(windowsCleanMountPath(path), requested) {
					return volume, path, nil
				}
			}
		}
		return platform.VolumeInfo{}, "", fmt.Errorf("Windows volume mount point %q was not discovered; run mountvol and verify it is local and mounted", requested)
	}

	fmt.Fprintln(os.Stdout, "Local Windows volumes:")
	for index, volume := range volumes {
		paths := strings.Join(volume.Paths, ", ")
		if paths == "" {
			paths = "(not mounted)"
		}
		kind := "fixed"
		if volume.Removable {
			kind = "removable"
		}
		fmt.Fprintf(os.Stdout, "  %d. %s | %s | %s | %.1f GiB total, %.1f GiB free | %s\n",
			index+1, paths, emptyDisplay(volume.Label), volume.Filesystem,
			float64(volume.TotalBytes)/(1<<30), float64(volume.FreeBytes)/(1<<30), kind)
	}
	fmt.Fprint(os.Stdout, "Select the physical volume number: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return platform.VolumeInfo{}, "", fmt.Errorf("read volume selection: %w; use --volume D:\\ for non-interactive init", err)
	}
	selection, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || selection < 1 || selection > len(volumes) {
		return platform.VolumeInfo{}, "", fmt.Errorf("volume selection must be a number from 1 to %d", len(volumes))
	}
	volume := volumes[selection-1]
	if len(volume.Paths) == 0 {
		return platform.VolumeInfo{}, "", fmt.Errorf("selected volume has no drive letter or mounted-folder path; assign one in Disk Management first")
	}
	paths := append([]string(nil), volume.Paths...)
	sort.SliceStable(paths, func(i, j int) bool {
		return isDriveRoot(paths[i]) && !isDriveRoot(paths[j])
	})
	return volume, paths[0], nil
}

func applyWindowsVolume(document *yaml.Node, physical *yaml.Node, volume platform.VolumeInfo, target, base string) {
	target = windowsCleanMountPath(target)
	setWindowsMappingValue(physical, "volume-guid", volume.GUID)
	setWindowsMappingValue(physical, "volume-label", volume.Label)
	setWindowsMappingValue(physical, "filesystem", volume.Filesystem)
	setWindowsMappingValue(physical, "target", target)
	setWindowsMappingValue(physical, "probe-path", base)

	root := document.Content[0]
	jellyfin := windowsMappingValue(root, "jellyfin")
	if jellyfin == nil || jellyfin.Kind != yaml.MappingNode {
		return
	}
	setWindowsMappingValue(jellyfin, "data-dir", filepath.Join(base, "data"))
	setWindowsMappingValue(jellyfin, "config-dir", filepath.Join(base, "config"))
	setWindowsMappingValue(jellyfin, "cache-dir", filepath.Join(base, "cache"))
	setWindowsMappingValue(jellyfin, "log-dir", filepath.Join(base, "log"))
}

func windowsDataRoot(target, requested string) (string, error) {
	target = windowsCleanMountPath(target)
	if requested == "" {
		return filepath.Join(target, "jellyfin"), nil
	}
	requested = filepath.Clean(strings.TrimSpace(requested))
	if !filepath.IsAbs(requested) {
		return "", fmt.Errorf("--data-root must be an absolute Windows path")
	}
	relative, err := filepath.Rel(target, requested)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, `..\`) {
		return "", fmt.Errorf("--data-root %s must be a directory beneath selected volume target %s", requested, target)
	}
	return requested, nil
}

func firstPhysicalDisk(document *yaml.Node) *yaml.Node {
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	disks := windowsMappingValue(document.Content[0], "disk")
	if disks == nil || disks.Kind != yaml.SequenceNode {
		return nil
	}
	for _, disk := range disks.Content {
		if disk.Kind == yaml.MappingNode {
			kind := windowsMappingValue(disk, "type")
			if kind != nil && strings.EqualFold(kind.Value, "physical") {
				return disk
			}
		}
	}
	return nil
}

func windowsMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func setWindowsMappingValue(mapping *yaml.Node, key, value string) {
	node := windowsMappingValue(mapping, key)
	if node == nil {
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.SingleQuotedStyle})
		return
	}
	node.Kind = yaml.ScalarNode
	node.Tag = "!!str"
	node.Value = value
	node.Style = yaml.SingleQuotedStyle
}

func windowsCleanMountPath(path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if isDriveRoot(path) {
		return strings.ToUpper(path[:1]) + `:\`
	}
	return path
}

func isDriveRoot(path string) bool {
	clean := filepath.Clean(path)
	return len(clean) == 3 && clean[1] == ':' && (clean[2] == '\\' || clean[2] == '/')
}

func emptyDisplay(value string) string {
	if value == "" {
		return "(no label)"
	}
	return value
}
