package kickstart

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

type Installation struct {
	Executable string
	WebDir     string
	Archive    *ArchiveInfo
}

func DiscoverInstalled() (Installation, bool) {
	for _, executable := range platformInstallCandidates() {
		info, err := os.Stat(executable)
		if err != nil || info.IsDir() {
			continue
		}
		if err := ValidateBinary(executable, runtime.GOOS, runtime.GOARCH); err != nil {
			continue
		}
		return Installation{Executable: executable, WebDir: discoverWebDir(executable)}, true
	}
	return Installation{}, false
}

func discoverWebDir(executable string) string {
	directory := filepath.Dir(executable)
	for _, candidate := range append(platformWebCandidates(executable),
		filepath.Join(directory, "jellyfin-web"),
		filepath.Join(directory, "wwwroot"),
		filepath.Join(filepath.Dir(directory), "jellyfin-web"),
		"/usr/share/jellyfin/web",
		"/usr/share/jellyfin-web",
	) {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(filepath.Join(candidate, "index.html")); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func ValidateBinary(path, wantOS, wantArch string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open Jellyfin executable: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	return validateBinaryReader(file, info.Size(), wantOS, wantArch)
}

func validateBinaryBytes(data []byte, wantOS, wantArch string) error {
	return validateBinaryReader(bytes.NewReader(data), int64(len(data)), wantOS, wantArch)
}

func validateBinaryReader(reader io.ReaderAt, size int64, wantOS, wantArch string) error {
	if size < 4 {
		return errors.New("Jellyfin executable is too small")
	}
	if f, err := elf.NewFile(reader); err == nil {
		if wantOS != "linux" {
			return fmt.Errorf("archive contains a Linux ELF executable, not %s", wantOS)
		}
		arch := mapELFArch(f.Machine)
		if arch != wantArch {
			return fmt.Errorf("Jellyfin executable architecture is %s, current system is %s", arch, wantArch)
		}
		return nil
	}
	if f, err := macho.NewFile(reader); err == nil {
		defer f.Close()
		if wantOS != "darwin" {
			return fmt.Errorf("archive contains a macOS Mach-O executable, not %s", wantOS)
		}
		arch := mapMachOArch(f.Cpu)
		if arch != wantArch {
			return fmt.Errorf("Jellyfin executable architecture is %s, current system is %s", arch, wantArch)
		}
		return nil
	}
	if f, err := macho.NewFatFile(reader); err == nil {
		defer f.Close()
		if wantOS != "darwin" {
			return fmt.Errorf("archive contains a macOS universal executable, not %s", wantOS)
		}
		for _, architecture := range f.Arches {
			if mapMachOArch(architecture.Cpu) == wantArch {
				return nil
			}
		}
		return fmt.Errorf("macOS universal executable does not contain %s", wantArch)
	}
	if f, err := pe.NewFile(reader); err == nil {
		defer f.Close()
		if wantOS != "windows" {
			return fmt.Errorf("archive contains a Windows PE executable, not %s", wantOS)
		}
		arch := mapPEArch(f.Machine)
		if arch != wantArch {
			return fmt.Errorf("Jellyfin executable architecture is %s, current system is %s", arch, wantArch)
		}
		return nil
	}
	return errors.New("Jellyfin executable is not a recognized ELF, Mach-O, or PE binary")
}

func mapELFArch(machine elf.Machine) string {
	switch machine {
	case elf.EM_X86_64:
		return "amd64"
	case elf.EM_AARCH64:
		return "arm64"
	default:
		return machine.String()
	}
}

func mapMachOArch(cpu macho.Cpu) string {
	switch cpu {
	case macho.CpuAmd64:
		return "amd64"
	case macho.CpuArm64:
		return "arm64"
	default:
		return cpu.String()
	}
}

func mapPEArch(machine uint16) string {
	switch machine {
	case pe.IMAGE_FILE_MACHINE_AMD64:
		return "amd64"
	case pe.IMAGE_FILE_MACHINE_ARM64:
		return "arm64"
	default:
		return fmt.Sprintf("PE machine %#x", machine)
	}
}
