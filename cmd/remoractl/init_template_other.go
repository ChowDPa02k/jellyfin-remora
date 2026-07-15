//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

func preparePlatformTemplate(template []byte, requestedVolume, requestedDataRoot string) ([]byte, error) {
	if requestedVolume != "" {
		return nil, fmt.Errorf("--volume is supported only on Windows")
	}
	if requestedDataRoot != "" {
		return nil, fmt.Errorf("--data-root is supported only on Windows")
	}
	return template, nil
}

func preparePlatformInitDirectories(cfg *config.Config, _ map[int]bool) error {
	if len(cfg.Disks) == 0 {
		return nil
	}
	for _, path := range []string{cfg.Jellyfin.DataDir, cfg.Jellyfin.ConfigDir, cfg.Jellyfin.CacheDir, cfg.Jellyfin.LogDir} {
		disk, ok := configuredStorageForPath(path, cfg.Disks)
		if !ok {
			return fmt.Errorf("refusing to prepare %s: it is not beneath verified configured storage", path)
		}
		if err := verifyStorageBoundary(path, disk.Target); err != nil {
			return err
		}
		if st, err := os.Stat(path); err == nil {
			if !st.IsDir() {
				return fmt.Errorf("Jellyfin path is not a directory: %s", path)
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}

		created, err := missingDirectories(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(path, 0o750); err != nil {
			return fmt.Errorf("create %s: %w", path, err)
		}
		if err := setPreparedDirectoryOwnership(cfg, disk, created); err != nil {
			return err
		}
		if err := verifyStorageBoundary(path, disk.Target); err != nil {
			return err
		}
	}
	return nil
}

func missingDirectories(path string) ([]string, error) {
	var missing []string
	candidate := filepath.Clean(path)
	for {
		if _, err := os.Stat(candidate); err == nil {
			return missing, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		missing = append(missing, candidate)
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return nil, fmt.Errorf("no existing ancestor for %s", path)
		}
		candidate = parent
	}
}

func setPreparedDirectoryOwnership(cfg *config.Config, disk config.DiskConfig, paths []string) error {
	if os.Geteuid() != 0 || disk.Type != "physical" || cfg.Jellyfin.RunAsUser == "" {
		return nil
	}
	account, err := user.Lookup(cfg.Jellyfin.RunAsUser)
	if err != nil {
		return fmt.Errorf("lookup jellyfin.run-as-user: %w", err)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return fmt.Errorf("parse UID for %s: %w", account.Username, err)
	}
	gidText := account.Gid
	if cfg.Jellyfin.RunAsGroup != "" {
		group, err := user.LookupGroup(cfg.Jellyfin.RunAsGroup)
		if err != nil {
			return fmt.Errorf("lookup jellyfin.run-as-group: %w", err)
		}
		gidText = group.Gid
	}
	gid, err := strconv.Atoi(gidText)
	if err != nil {
		return fmt.Errorf("parse GID for %s: %w", account.Username, err)
	}
	for _, path := range paths {
		if err := os.Chown(path, uid, gid); err != nil {
			return fmt.Errorf("set Jellyfin ownership on %s: %w", path, err)
		}
	}
	return nil
}

func configuredStorageForPath(path string, disks []config.DiskConfig) (config.DiskConfig, bool) {
	var selected config.DiskConfig
	selectedLength := -1
	for _, disk := range disks {
		if sameFileAncestor(disk.Target, path) && len(filepath.Clean(disk.Target)) > selectedLength {
			selected = disk
			selectedLength = len(filepath.Clean(disk.Target))
		}
	}
	return selected, selectedLength >= 0
}

func verifyStorageBoundary(path, target string) error {
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return fmt.Errorf("resolve configured storage target %s: %w", target, err)
	}
	ancestor, err := nearestExistingAncestor(path)
	if err != nil {
		return fmt.Errorf("inspect storage boundary for %s: %w", path, err)
	}
	resolvedAncestor, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return fmt.Errorf("resolve storage path ancestor %s: %w", ancestor, err)
	}
	if !sameFileAncestor(resolvedTarget, resolvedAncestor) {
		return fmt.Errorf("storage path %s resolves outside configured target %s", path, target)
	}
	return nil
}

func nearestExistingAncestor(path string) (string, error) {
	candidate := filepath.Clean(path)
	for {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return "", fmt.Errorf("no existing ancestor for %s", path)
		}
		candidate = parent
	}
}

func sameFileAncestor(root, path string) bool {
	rootInfo, err := os.Stat(root)
	if err != nil {
		return false
	}
	candidate := filepath.Clean(path)
	for {
		if info, err := os.Stat(candidate); err == nil {
			if os.SameFile(rootInfo, info) {
				return true
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return false
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return false
		}
		candidate = parent
	}
}

func initExecutableUsable(info os.FileInfo) bool {
	return !info.IsDir() && info.Mode().Perm()&0o111 != 0
}
