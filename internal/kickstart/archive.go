package kickstart

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ulikunitz/xz"
)

const (
	maxArchiveExecutableSize = 256 << 20
	maxArchiveFileSize       = int64(512 << 20)
	maxArchiveExpandedSize   = int64(4 << 30)
	maxArchiveEntries        = 100_000
)

type archiveBudget struct {
	entries int
	total   int64
}

func (b *archiveBudget) account(name string, size int64) error {
	if size < 0 {
		return fmt.Errorf("archive entry %s has a negative size", name)
	}
	b.entries++
	if b.entries > maxArchiveEntries {
		return fmt.Errorf("archive contains more than %d entries", maxArchiveEntries)
	}
	if size > maxArchiveFileSize {
		return fmt.Errorf("archive entry %s exceeds the 512 MiB file limit", name)
	}
	if size > maxArchiveExpandedSize-b.total {
		return errors.New("archive exceeds the 4 GiB expanded-size limit")
	}
	b.total += size
	return nil
}

type ArchiveInfo struct {
	Path            string
	ExecutableEntry string
	WebDirEntry     string
	VerifiedSHA256  string
	VerifiedSize    int64
}

func InspectArchive(path string) (ArchiveInfo, error) {
	path, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return ArchiveInfo{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return ArchiveInfo{}, fmt.Errorf("inspect Jellyfin archive: %w", err)
	}
	if info.IsDir() {
		return ArchiveInfo{}, errors.New("Jellyfin Generic package must be a .tar.gz, .tar.xz, or .zip file")
	}
	if strings.EqualFold(filepath.Ext(path), ".zip") {
		return inspectZip(path)
	}
	return inspectTar(path)
}

func inspectZip(path string) (ArchiveInfo, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return ArchiveInfo{}, fmt.Errorf("open Jellyfin ZIP: %w", err)
	}
	defer reader.Close()
	result := ArchiveInfo{Path: path}
	var binaryErrors []string
	var budget archiveBudget
	for _, entry := range reader.File {
		name, err := safeArchiveName(entry.Name)
		if err != nil {
			return ArchiveInfo{}, err
		}
		if err := budget.account(name, int64(entry.UncompressedSize64)); err != nil {
			return ArchiveInfo{}, err
		}
		if isWebIndex(name) && result.WebDirEntry == "" {
			result.WebDirEntry = filepath.Dir(name)
		}
		if result.ExecutableEntry != "" || !isJellyfinExecutable(name) || entry.FileInfo().IsDir() {
			continue
		}
		stream, err := entry.Open()
		if err != nil {
			return ArchiveInfo{}, err
		}
		data, readErr := readExecutable(stream, int64(entry.UncompressedSize64))
		stream.Close()
		if readErr != nil {
			binaryErrors = append(binaryErrors, name+": "+readErr.Error())
			continue
		}
		if err := validateBinaryBytes(data, runtime.GOOS, runtime.GOARCH); err != nil {
			binaryErrors = append(binaryErrors, name+": "+err.Error())
			continue
		}
		result.ExecutableEntry = name
	}
	return finishArchiveInspection(result, binaryErrors)
}

func inspectTar(path string) (ArchiveInfo, error) {
	stream, reader, err := openTar(path)
	if err != nil {
		return ArchiveInfo{}, err
	}
	defer stream.Close()
	result := ArchiveInfo{Path: path}
	var binaryErrors []string
	var budget archiveBudget
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ArchiveInfo{}, fmt.Errorf("read Jellyfin tar archive: %w", err)
		}
		name, err := safeArchiveName(header.Name)
		if err != nil {
			return ArchiveInfo{}, err
		}
		if err := budget.account(name, header.Size); err != nil {
			return ArchiveInfo{}, err
		}
		if isWebIndex(name) && result.WebDirEntry == "" {
			result.WebDirEntry = filepath.Dir(name)
		}
		if result.ExecutableEntry != "" || !isJellyfinExecutable(name) || header.Typeflag != tar.TypeReg {
			continue
		}
		data, readErr := readExecutable(reader, header.Size)
		if readErr != nil {
			binaryErrors = append(binaryErrors, name+": "+readErr.Error())
			continue
		}
		if err := validateBinaryBytes(data, runtime.GOOS, runtime.GOARCH); err != nil {
			binaryErrors = append(binaryErrors, name+": "+err.Error())
			continue
		}
		result.ExecutableEntry = name
	}
	return finishArchiveInspection(result, binaryErrors)
}

func finishArchiveInspection(result ArchiveInfo, binaryErrors []string) (ArchiveInfo, error) {
	if result.ExecutableEntry == "" {
		detail := "no Jellyfin executable was found"
		if len(binaryErrors) > 0 {
			detail = strings.Join(binaryErrors, "; ")
		}
		return ArchiveInfo{}, fmt.Errorf("Generic package is incompatible with %s/%s: %s", runtime.GOOS, runtime.GOARCH, detail)
	}
	return result, nil
}

func readExecutable(reader io.Reader, size int64) ([]byte, error) {
	if size <= 0 || size > maxArchiveExecutableSize {
		return nil, fmt.Errorf("executable size %d is outside the supported range", size)
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxArchiveExecutableSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxArchiveExecutableSize {
		return nil, errors.New("executable exceeds 256 MiB")
	}
	return data, nil
}

func ExtractArchive(info ArchiveInfo, destination string) (Installation, error) {
	return extractArchive(info, destination, nil)
}

func extractArchive(info ArchiveInfo, destination string, afterVerification func() error) (Installation, error) {
	archive, err := os.Open(info.Path)
	if err != nil {
		return Installation{}, fmt.Errorf("open selected Jellyfin package: %w", err)
	}
	defer archive.Close()
	if err := verifySelectedArchive(info, archive); err != nil {
		return Installation{}, err
	}
	if afterVerification != nil {
		if err := afterVerification(); err != nil {
			return Installation{}, err
		}
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return Installation{}, fmt.Errorf("rewind selected Jellyfin package: %w", err)
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return Installation{}, err
	}
	if err := requireEmptyDestination(destination); err != nil {
		return Installation{}, err
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return Installation{}, err
	}
	stage, err := os.MkdirTemp(parent, ".jellyfin-kickstart-*")
	if err != nil {
		return Installation{}, err
	}
	defer os.RemoveAll(stage)
	if strings.EqualFold(filepath.Ext(info.Path), ".zip") {
		err = extractZip(archive, info.VerifiedSize, stage)
	} else {
		err = extractTar(archive, info.Path, stage)
	}
	if err != nil {
		return Installation{}, err
	}
	if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Installation{}, err
	}
	if err := os.Rename(stage, destination); err != nil {
		return Installation{}, fmt.Errorf("install extracted Jellyfin package: %w", err)
	}
	executable := filepath.Join(destination, info.ExecutableEntry)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(executable, 0o750); err != nil {
			return Installation{}, fmt.Errorf("make extracted Jellyfin executable runnable: %w", err)
		}
	}
	if err := ValidateBinary(executable, runtime.GOOS, runtime.GOARCH); err != nil {
		return Installation{}, fmt.Errorf("validate extracted Jellyfin executable: %w", err)
	}
	web := ""
	if info.WebDirEntry != "" {
		web = filepath.Join(destination, info.WebDirEntry)
	}
	return Installation{Executable: executable, WebDir: web}, nil
}

func verifySelectedArchive(info ArchiveInfo, archive io.Reader) error {
	digest, err := hex.DecodeString(info.VerifiedSHA256)
	if err != nil || len(digest) != 32 || info.VerifiedSize <= 0 {
		return errors.New("selected Jellyfin package does not have a valid verified SHA-256 digest and size")
	}
	hash, size, err := hashPackageReader(archive)
	if err != nil {
		return fmt.Errorf("recheck selected Jellyfin package: %w", err)
	}
	if !strings.EqualFold(hash, info.VerifiedSHA256) || size != info.VerifiedSize {
		return errors.New("selected Jellyfin package changed after repository verification")
	}
	return nil
}

func requireEmptyDestination(path string) error {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("Generic package destination is not empty: %s", path)
	}
	return nil
}

func extractZip(archive io.ReaderAt, size int64, root string) error {
	reader, err := zip.NewReader(archive, size)
	if err != nil {
		return err
	}
	var budget archiveBudget
	for _, entry := range reader.File {
		name, err := safeArchiveName(entry.Name)
		if err != nil {
			return err
		}
		if err := budget.account(name, int64(entry.UncompressedSize64)); err != nil {
			return err
		}
		target := filepath.Join(root, name)
		mode := entry.Mode()
		if mode.IsDir() {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
			continue
		}
		if mode&os.ModeSymlink != 0 {
			stream, err := entry.Open()
			if err != nil {
				return err
			}
			link, readErr := io.ReadAll(io.LimitReader(stream, 4097))
			stream.Close()
			if readErr != nil {
				return readErr
			}
			if err := createSafeSymlink(root, name, string(link)); err != nil {
				return err
			}
			continue
		}
		stream, err := entry.Open()
		if err != nil {
			return err
		}
		if err := writeArchiveFile(target, stream, mode, int64(entry.UncompressedSize64)); err != nil {
			stream.Close()
			return err
		}
		stream.Close()
	}
	return nil
}

func extractTar(archive io.Reader, path, root string) error {
	reader, decoder, err := openTarReader(archive, path)
	if err != nil {
		return err
	}
	if decoder != nil {
		defer decoder.Close()
	}
	var budget archiveBudget
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name, err := safeArchiveName(header.Name)
		if err != nil {
			return err
		}
		if err := budget.account(name, header.Size); err != nil {
			return err
		}
		target := filepath.Join(root, name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := writeArchiveFile(target, reader, os.FileMode(header.Mode), header.Size); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := createSafeSymlink(root, name, header.Linkname); err != nil {
				return err
			}
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
		default:
			return fmt.Errorf("unsupported archive entry type %d for %s", header.Typeflag, name)
		}
	}
}

func writeArchiveFile(path string, reader io.Reader, sourceMode os.FileMode, expectedSize int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	mode := os.FileMode(0o640)
	if sourceMode&0o111 != 0 {
		mode = 0o750
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(reader, maxArchiveFileSize+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if written != expectedSize {
		return fmt.Errorf("archive entry size changed while extracting: expected %d bytes, got %d", expectedSize, written)
	}
	return closeErr
}

func createSafeSymlink(root, name, link string) error {
	if strings.ContainsRune(link, 0) || filepath.IsAbs(filepath.FromSlash(link)) {
		return fmt.Errorf("archive symlink %s has unsafe target %q", name, link)
	}
	resolved, err := safeArchiveName(filepath.ToSlash(filepath.Join(filepath.Dir(name), filepath.FromSlash(link))))
	if err != nil || resolved == "." {
		return fmt.Errorf("archive symlink %s escapes extraction root", name)
	}
	target := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	return os.Symlink(filepath.FromSlash(link), target)
}

func safeArchiveName(name string) (string, error) {
	name = filepath.Clean(filepath.FromSlash(strings.TrimSpace(name)))
	if name == "." {
		return name, nil
	}
	if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) || filepath.VolumeName(name) != "" {
		return "", fmt.Errorf("archive entry escapes extraction root: %q", name)
	}
	return name, nil
}

func isJellyfinExecutable(name string) bool {
	base := strings.ToLower(filepath.Base(name))
	return base == "jellyfin" || base == "jellyfin.exe" || base == "jellyfin server"
}

func isWebIndex(name string) bool {
	if !strings.EqualFold(filepath.Base(name), "index.html") {
		return false
	}
	parent := strings.ToLower(filepath.Base(filepath.Dir(name)))
	return parent == "jellyfin-web" || parent == "wwwroot"
}

func openTar(path string) (*os.File, *tar.Reader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	reader, _, err := openTarReader(file, path)
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, reader, nil
}

func openTarReader(archive io.Reader, path string) (*tar.Reader, io.Closer, error) {
	var source io.Reader = archive
	var closer io.Closer
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		gzipReader, err := gzip.NewReader(archive)
		if err != nil {
			return nil, nil, err
		}
		source = gzipReader
		closer = gzipReader
	case strings.HasSuffix(lower, ".tar.xz"), strings.HasSuffix(lower, ".txz"):
		xzReader, err := xz.NewReader(archive)
		if err != nil {
			return nil, nil, err
		}
		source = xzReader
	default:
		return nil, nil, errors.New("Jellyfin Generic package must use .tar.gz, .tar.xz, or .zip")
	}
	return tar.NewReader(source), closer, nil
}
