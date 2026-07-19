package kickstart

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func FuzzArchiveEntryName(f *testing.F) {
	f.Add("jellyfin/jellyfin")
	f.Add("../jellyfin")
	f.Add("jellyfin/../../escape")
	f.Add("/absolute/jellyfin")
	f.Fuzz(func(t *testing.T, name string) {
		if len(name) > 64*1024 {
			t.Skip()
		}
		clean, err := safeArchiveName(name)
		if err != nil {
			return
		}
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.VolumeName(clean) != "" {
			t.Fatalf("unsafe archive name accepted: input=%q clean=%q", name, clean)
		}
	})
}

func FuzzInspectArchiveBytes(f *testing.F) {
	f.Add("zip", []byte("PK\x03\x04"))
	f.Add("tar.gz", []byte{0x1f, 0x8b, 0x08})
	f.Add("tar.xz", []byte{0xfd, '7', 'z', 'X', 'Z', 0})
	f.Add("zip", zipWithDeclaredSizes(maxArchiveFileSize+1))
	f.Add("zip", zipWithDeclaredSizes(
		maxArchiveFileSize, maxArchiveFileSize, maxArchiveFileSize, maxArchiveFileSize,
		maxArchiveFileSize, maxArchiveFileSize, maxArchiveFileSize, maxArchiveFileSize, 1,
	))
	f.Fuzz(func(t *testing.T, kind string, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		extension := ".zip"
		switch kind {
		case "tar.gz":
			extension = ".tar.gz"
		case "tar.xz":
			extension = ".tar.xz"
		}
		path := filepath.Join(t.TempDir(), "archive"+extension)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, inspectErr := InspectArchive(path)
		if limit := declaredZIPLimit(data); limit != "" {
			if inspectErr == nil || !strings.Contains(inspectErr.Error(), limit) {
				t.Fatalf("declared archive limit %q was not enforced: %v", limit, inspectErr)
			}
		}
	})
}

func TestArchiveLimitFailureCleansExtractionStage(t *testing.T) {
	parent := t.TempDir()
	archivePath := filepath.Join(parent, "oversized.tar.gz")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(file)
	writer := tar.NewWriter(compressed)
	if err := writer.WriteHeader(&tar.Header{Name: "jellyfin/asset", Mode: 0o640, Size: maxArchiveFileSize + 1, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close() // Deliberately incomplete payload; the limit is checked from the header.
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(contents))
	destination := filepath.Join(parent, "server")
	_, err = ExtractArchive(ArchiveInfo{
		Path: archivePath, ExecutableEntry: "jellyfin/jellyfin",
		VerifiedSHA256: digest, VerifiedSize: int64(len(contents)),
	}, destination)
	if err == nil || !strings.Contains(err.Error(), "512 MiB") {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("failed extraction left destination behind: %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".jellyfin-kickstart-") {
			t.Fatalf("failed extraction left staging directory %s", entry.Name())
		}
	}
}

func zipWithDeclaredSizes(sizes ...int64) []byte {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for index := range sizes {
		entry, _ := writer.Create(fmt.Sprintf("asset-%d", index))
		_, _ = entry.Write([]byte{'x'})
	}
	_ = writer.Close()
	data := buffer.Bytes()
	searchFrom := 0
	for _, size := range sizes {
		relative := bytes.Index(data[searchFrom:], []byte{'P', 'K', 1, 2})
		if relative < 0 {
			return data
		}
		central := searchFrom + relative
		binary.LittleEndian.PutUint32(data[central+24:], uint32(size))
		searchFrom = central + 46
	}
	return data
}

func declaredZIPLimit(data []byte) string {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return ""
	}
	var budget archiveBudget
	for _, entry := range reader.File {
		if err := budget.account(entry.Name, int64(entry.UncompressedSize64)); err != nil {
			switch {
			case strings.Contains(err.Error(), "512 MiB"):
				return "512 MiB"
			case strings.Contains(err.Error(), "4 GiB"):
				return "4 GiB"
			case strings.Contains(err.Error(), "100000"):
				return "100000"
			}
		}
	}
	return ""
}
