package kickstart

import (
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
		_, _ = InspectArchive(path)
	})
}
