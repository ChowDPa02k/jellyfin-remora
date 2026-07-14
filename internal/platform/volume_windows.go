//go:build windows

package platform

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const (
	driveRemovable = 2
	driveFixed     = 3
)

var (
	procFindFirstVolumeW                 = kernel32.NewProc("FindFirstVolumeW")
	procFindNextVolumeW                  = kernel32.NewProc("FindNextVolumeW")
	procFindVolumeClose                  = kernel32.NewProc("FindVolumeClose")
	procGetVolumePathNamesForVolumeNameW = kernel32.NewProc("GetVolumePathNamesForVolumeNameW")
	procGetDiskFreeSpaceExW              = kernel32.NewProc("GetDiskFreeSpaceExW")
	procGetVolumePathNameW               = kernel32.NewProc("GetVolumePathNameW")
)

// VolumeGUIDForPath resolves an existing path through drive letters, mounted
// folders, and reparse points to the volume that actually contains it.
func VolumeGUIDForPath(path string) (string, error) {
	full, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	input, err := syscall.UTF16PtrFromString(full)
	if err != nil {
		return "", err
	}
	buffer := make([]uint16, 32768)
	r1, _, callErr := procGetVolumePathNameW.Call(
		uintptr(unsafe.Pointer(input)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if r1 == 0 {
		return "", fmt.Errorf("GetVolumePathNameW %s: %w", path, errnoError(callErr))
	}
	mountPoint := syscall.UTF16ToString(buffer)
	guid, err := volumeNameForMountPoint(mountPoint)
	if err != nil {
		return "", fmt.Errorf("resolve volume for %s through %s: %w", path, mountPoint, err)
	}
	return guid, nil
}

type VolumeInfo struct {
	GUID       string
	Paths      []string
	Label      string
	Filesystem string
	TotalBytes uint64
	FreeBytes  uint64
	Removable  bool
}

func DiscoverVolumes() ([]VolumeInfo, error) {
	buffer := make([]uint16, 1024)
	handle, _, callErr := procFindFirstVolumeW.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if handle == ^uintptr(0) {
		return nil, fmt.Errorf("FindFirstVolumeW: %w", errnoError(callErr))
	}
	defer procFindVolumeClose.Call(handle)

	var volumes []VolumeInfo
	for {
		guid := syscall.UTF16ToString(buffer)
		if info, err := inspectLocalVolume(guid); err == nil {
			volumes = append(volumes, info)
		}
		for i := range buffer {
			buffer[i] = 0
		}
		r1, _, nextErr := procFindNextVolumeW.Call(handle, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
		if r1 == 0 {
			if errors.Is(nextErr, syscall.ERROR_NO_MORE_FILES) {
				break
			}
			return nil, fmt.Errorf("FindNextVolumeW: %w", errnoError(nextErr))
		}
	}
	return volumes, nil
}

func inspectLocalVolume(guid string) (VolumeInfo, error) {
	root, err := syscall.UTF16PtrFromString(ensureTrailingBackslash(guid))
	if err != nil {
		return VolumeInfo{}, err
	}
	driveType, _, _ := procGetDriveTypeW.Call(uintptr(unsafe.Pointer(root)))
	if driveType != driveFixed && driveType != driveRemovable {
		return VolumeInfo{}, fmt.Errorf("volume %s is not a local fixed or removable disk", guid)
	}
	label, filesystem, err := volumeInformation(guid)
	if err != nil {
		return VolumeInfo{}, err
	}
	paths, err := volumePaths(guid)
	if err != nil {
		return VolumeInfo{}, err
	}
	var available, total, free uint64
	r1, _, spaceErr := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(root)), uintptr(unsafe.Pointer(&available)),
		uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&free)))
	if r1 == 0 {
		return VolumeInfo{}, fmt.Errorf("GetDiskFreeSpaceExW %s: %w", guid, errnoError(spaceErr))
	}
	return VolumeInfo{
		GUID: guid, Paths: paths, Label: label, Filesystem: filesystem,
		TotalBytes: total, FreeBytes: free, Removable: driveType == driveRemovable,
	}, nil
}

func volumePaths(guid string) ([]string, error) {
	volume, err := syscall.UTF16PtrFromString(ensureTrailingBackslash(guid))
	if err != nil {
		return nil, err
	}
	buffer := make([]uint16, 32768)
	var required uint32
	r1, _, callErr := procGetVolumePathNamesForVolumeNameW.Call(
		uintptr(unsafe.Pointer(volume)), uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)), uintptr(unsafe.Pointer(&required)))
	if r1 == 0 {
		return nil, fmt.Errorf("GetVolumePathNamesForVolumeNameW %s: %w", guid, errnoError(callErr))
	}
	if required > uint32(len(buffer)) {
		return nil, fmt.Errorf("volume %s returned an oversized mount-path list", guid)
	}
	return splitUTF16MultiString(buffer[:required]), nil
}

func splitUTF16MultiString(buffer []uint16) []string {
	var values []string
	for start := 0; start < len(buffer); {
		end := start
		for end < len(buffer) && buffer[end] != 0 {
			end++
		}
		if end == start {
			break
		}
		value := strings.TrimSpace(syscall.UTF16ToString(buffer[start:end]))
		if value != "" {
			values = append(values, value)
		}
		start = end + 1
	}
	return values
}
