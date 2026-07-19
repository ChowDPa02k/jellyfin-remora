package kickstart

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInspectAndExtractTarGZ(t *testing.T) {
	binary, err := os.ReadFile(matchingExecutable(t))
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "jellyfin.tar.gz")
	writeTarGZ(t, archive, map[string][]byte{
		"jellyfin/jellyfin":                binary,
		"jellyfin/jellyfin-web/index.html": []byte("<html></html>"),
	})
	info, err := InspectArchive(archive)
	if err != nil {
		t.Fatal(err)
	}
	info.VerifiedSHA256, info.VerifiedSize, err = hashPackageFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "server")
	installation, err := ExtractArchive(info, destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateBinary(installation.Executable, runtime.GOOS, runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(installation.WebDir, "index.html")); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(installation.Executable)
		if err != nil || info.Mode()&0o111 == 0 {
			t.Fatalf("extracted executable is not runnable: %v %#o", err, info.Mode())
		}
	}
}

func TestInspectAndExtractZIP(t *testing.T) {
	binary, err := os.ReadFile(matchingExecutable(t))
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "jellyfin.zip")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, content := range map[string][]byte{
		"jellyfin/jellyfin": binary, "jellyfin/jellyfin-web/index.html": []byte("web"),
	} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := InspectArchive(archive)
	if err != nil {
		t.Fatal(err)
	}
	info.VerifiedSHA256, info.VerifiedSize, err = hashPackageFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := ExtractArchive(info, filepath.Join(t.TempDir(), "server"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateBinary(installation.Executable, runtime.GOOS, runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
}

func TestValidateBinaryRejectsWrongArchitecture(t *testing.T) {
	binary, err := os.ReadFile(matchingExecutable(t))
	if err != nil {
		t.Fatal(err)
	}
	wrong := "amd64"
	if runtime.GOARCH == "amd64" {
		wrong = "arm64"
	}
	if err := validateBinaryBytes(binary, runtime.GOOS, wrong); err == nil {
		t.Fatal("expected architecture mismatch")
	}
}

func TestInspectArchiveRejectsTraversal(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "unsafe.tar.gz")
	writeTarGZ(t, archive, map[string][]byte{"../jellyfin": []byte("unsafe")})
	if _, err := InspectArchive(archive); err == nil {
		t.Fatal("expected path traversal rejection")
	}
}

func TestExtractArchiveRejectsPackageChangedAfterVerification(t *testing.T) {
	binary, err := os.ReadFile(matchingExecutable(t))
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "jellyfin_10.11.11-test.tar.gz")
	writeTarGZ(t, archive, map[string][]byte{"jellyfin/jellyfin": binary})
	info, err := InspectArchive(archive)
	if err != nil {
		t.Fatal(err)
	}
	info.VerifiedSHA256, info.VerifiedSize, err = hashPackageFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(archive, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("changed")); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractArchive(info, filepath.Join(t.TempDir(), "server")); err == nil || !strings.Contains(err.Error(), "changed after repository verification") {
		t.Fatalf("error=%v", err)
	}
}

func TestExtractArchiveRequiresVerifiedDigest(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "jellyfin.tar.gz")
	writeTarGZ(t, archive, map[string][]byte{"jellyfin/jellyfin": []byte("unverified")})
	destination := filepath.Join(t.TempDir(), "server")
	_, err := ExtractArchive(ArchiveInfo{Path: archive, ExecutableEntry: "jellyfin/jellyfin"}, destination)
	if err == nil || !strings.Contains(err.Error(), "verified SHA-256") {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("unverified archive created destination: %v", err)
	}
}

func TestExtractArchiveUsesVerifiedOpenFile(t *testing.T) {
	binary, err := os.ReadFile(matchingExecutable(t))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	archive := filepath.Join(directory, "jellyfin.tar.gz")
	replacement := filepath.Join(directory, "replacement.tar.gz")
	writeTarGZ(t, archive, map[string][]byte{"jellyfin/jellyfin": binary})
	writeTarGZ(t, replacement, map[string][]byte{"replacement": []byte("not Jellyfin")})
	info, err := InspectArchive(archive)
	if err != nil {
		t.Fatal(err)
	}
	info.VerifiedSHA256, info.VerifiedSize, err = hashPackageFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(directory, "server")
	installation, err := extractArchive(info, destination, func() error {
		return os.Rename(replacement, archive)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateBinary(installation.Executable, runtime.GOOS, runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveBudgetLimitsExpandedContent(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*archiveBudget) error
		want    string
	}{
		{
			name: "single file",
			prepare: func(b *archiveBudget) error {
				return b.account("oversized", maxArchiveFileSize+1)
			},
			want: "512 MiB",
		},
		{
			name: "expanded total",
			prepare: func(b *archiveBudget) error {
				for index := 0; index < 8; index++ {
					if err := b.account("part", maxArchiveFileSize); err != nil {
						return err
					}
				}
				return b.account("overflow", 1)
			},
			want: "4 GiB",
		},
		{
			name: "entry count",
			prepare: func(b *archiveBudget) error {
				b.entries = maxArchiveEntries
				return b.account("one-too-many", 0)
			},
			want: "100000 entries",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.prepare(&archiveBudget{}); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want substring %q", err, test.want)
			}
		})
	}
}

func matchingExecutable(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func writeTarGZ(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gz)
	for name, content := range files {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
