//go:build darwin

package platform

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestEnsureMountTargetCreatesMissingDirectory(t *testing.T) {
	target := filepath.Join(t.TempDir(), "nested", "mount")
	if err := ensureMountTarget(target); err != nil {
		t.Fatalf("ensureMountTarget() error = %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("target mode = %v, want directory", info.Mode())
	}
}

func TestDarwinNFSOptionsDefaultToNoRemoteLocks(t *testing.T) {
	tests := map[string]string{
		"":                         darwinDefaultNFSOptions,
		"soft,resvport":            "soft,resvport,nolocks",
		"vers=3,resvport":          "vers=3,resvport,nolocks",
		"nfsvers=2-3":              "nfsvers=2-3,nolocks",
		"vers=3,nolocks":           "vers=3,nolocks",
		"vers=3,locallocks":        "vers=3,locallocks",
		"vers=3,locks":             "vers=3,locks",
		"vers=4,resvport":          "vers=4,resvport",
		"nfsvers=4.0,soft":         "nfsvers=4.0,soft",
		"vers=3-4,resvport":        "vers=3-4,resvport",
		"nfsv4,resvport":           "nfsv4,resvport",
		"  vers=3,resvport  ":      "vers=3,resvport,nolocks",
		"vers=3,nolocallocks,soft": "vers=3,nolocallocks,soft",
	}
	for configured, want := range tests {
		if got := darwinNFSOptions(configured); got != want {
			t.Errorf("darwinNFSOptions(%q) = %q, want %q", configured, got, want)
		}
	}
}

func TestRequiredArgumentMatchingUsesTokenBoundaries(t *testing.T) {
	arguments := []string{"/Applications/Jellyfin", "--datadir=/Volumes/App Data", "--configdir=/config"}
	for _, arg := range []string{"--datadir=/Volumes/App Data", "--configdir=/config"} {
		if !hasRequiredArg(arguments, arg) {
			t.Fatalf("required argument %q did not match %q", arg, arguments)
		}
	}
	for _, arg := range []string{"--datadir=/Volumes/App", "--configdir=/conf", "figdir=/config"} {
		if hasRequiredArg(arguments, arg) {
			t.Fatalf("argument prefix %q matched %q", arg, arguments)
		}
	}
	if !hasRequiredArg([]string{"jellyfin", "--datadir", "/Volumes/App Data"}, "--datadir=/Volumes/App Data") {
		t.Fatal("split key/value argument form did not match")
	}
}

func TestParseElapsedProcessAge(t *testing.T) {
	for input, want := range map[string]time.Duration{
		"00:05":      5 * time.Second,
		"01:02:03":   time.Hour + 2*time.Minute + 3*time.Second,
		"2-03:04:05": 51*time.Hour + 4*time.Minute + 5*time.Second,
	} {
		got, err := parseElapsed(input)
		if err != nil || got != want {
			t.Fatalf("parseElapsed(%q) = %s, %v; want %s", input, got, err, want)
		}
	}
	if _, err := parseElapsed("1:99"); err == nil {
		t.Fatal("invalid elapsed time succeeded")
	}
}

func TestCountDescendantFFmpegOnlyCountsManagedTree(t *testing.T) {
	processes := `
100 1 /Applications/Jellyfin
101 100 /usr/local/bin/ffmpeg
102 101 /bin/helper
103 102 /opt/jellyfin-ffmpeg
200 1 /usr/local/bin/ffmpeg
`
	if got := countDescendantFFmpeg(processes, 100); got != 2 {
		t.Fatalf("ffmpeg descendants=%d, want 2", got)
	}
}

func TestDescendantPIDTreeIncludesEscapedNestedChildren(t *testing.T) {
	parents := map[int]int{101: 100, 102: 101, 103: 1, 104: 102}
	got := descendantPIDTree(parents, 100)
	want := []int{101, 102, 104}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("descendantPIDTree() = %v, want %v", got, want)
	}
}

func TestDarwinProcessTreeSignalsSnapshotWhenRootExited(t *testing.T) {
	var signaled []int
	err := signalDarwinProcessTree(
		100, []int{101, 102}, syscall.SIGKILL, nil,
		func(pid int) (int, error) {
			if pid == 100 {
				return 0, syscall.ESRCH
			}
			return 100, nil
		},
		func(pid int, _ syscall.Signal) error {
			signaled = append(signaled, pid)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(signaled, []int{101, 102}) {
		t.Fatalf("signaled = %v, want snapshotted descendants", signaled)
	}
}

func TestEnsureMountTargetRejectsUnsafeTargets(t *testing.T) {
	for _, target := range []string{"", "relative", string(filepath.Separator)} {
		t.Run(strings.ReplaceAll(target, string(filepath.Separator), "root"), func(t *testing.T) {
			if err := ensureMountTarget(target); err == nil {
				t.Fatalf("ensureMountTarget(%q) succeeded, want error", target)
			}
		})
	}
}

func TestEnsureMountTargetRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	realTarget := filepath.Join(root, "real")
	if err := os.Mkdir(realTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realTarget, link); err != nil {
		t.Fatal(err)
	}
	if err := ensureMountTarget(link); err == nil {
		t.Fatal("ensureMountTarget() succeeded for symlink, want error")
	}
}

func TestSameExecutableAcceptsSymlinkAndRejectsDifferentFile(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "jellyfin")
	other := filepath.Join(root, "other")
	if err := os.WriteFile(executable, []byte("jellyfin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("other"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "jellyfin-link")
	if err := os.Symlink(executable, link); err != nil {
		t.Fatal(err)
	}
	if !sameExecutable(link, executable) {
		t.Fatal("sameExecutable() rejected a symlink to the same file")
	}
	if sameExecutable(other, executable) {
		t.Fatal("sameExecutable() accepted a different file")
	}
}

func TestExecutableProvenanceDetectsDarwinXattr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jellyfin")
	if err := os.WriteFile(path, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	backend := &darwinBackend{}
	if err := unix.Setxattr(path, "com.apple.provenance", []byte{1, 2}, 0); err != nil {
		t.Fatal(err)
	}
	found, err := backend.ExecutableProvenance(path)
	if err != nil || !found {
		t.Fatalf("marked executable: found=%t err=%v", found, err)
	}
	if _, err := backend.ExecutableProvenance(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing executable provenance inspection succeeded")
	}
}
