package kickstart

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPackageValidatorPreservesRepositoryFailures(t *testing.T) {
	path := writeValidatorPackage(t, "jellyfin_10.11.11-arm64.tar.xz", []byte("package"))
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			defer server.Close()
			_, err := testPackageValidator(server).Validate(context.Background(), path, nil)
			var repositoryErr *PackageRepositoryError
			if !errors.As(err, &repositoryErr) || repositoryErr.StatusCode != status || errors.Is(err, ErrPackageNotFound) {
				t.Fatalf("error=%v repositoryError=%+v", err, repositoryErr)
			}
		})
	}
}

func TestPackageValidatorPreservesTimeoutWhenOtherCandidatesAreAbsent(t *testing.T) {
	path := writeValidatorPackage(t, "jellyfin_10.11.11-arm64.tar.xz", []byte("package"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/macos/stable/") && r.URL.RawQuery == "mirrorlist" {
			<-r.Context().Done()
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	validator := testPackageValidator(server)
	validator.RequestTimeout = 20 * time.Millisecond
	_, err := validator.Validate(context.Background(), path, nil)
	var repositoryErr *PackageRepositoryError
	if !errors.As(err, &repositoryErr) || !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrPackageNotFound) {
		t.Fatalf("error=%v repositoryError=%+v", err, repositoryErr)
	}
}

func TestPackageValidatorPreservesArchiveDownloadFailure(t *testing.T) {
	content := []byte("package-with-matching-size")
	path := writeValidatorPackage(t, "jellyfin_10.9.11-amd64.tar.xz", content)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/archive/server/linux/stable/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		if r.Method != http.MethodHead {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	_, err := testPackageValidator(server).Validate(context.Background(), path, nil)
	var repositoryErr *PackageRepositoryError
	if !errors.As(err, &repositoryErr) || repositoryErr.StatusCode != http.StatusServiceUnavailable || errors.Is(err, ErrPackageNotFound) {
		t.Fatalf("error=%v repositoryError=%+v", err, repositoryErr)
	}
}

func TestPackageValidatorMatchesPublishedMirrorlistHash(t *testing.T) {
	content := []byte("official-jellyfin-package")
	path := writeValidatorPackage(t, "jellyfin_10.11.11-arm64.tar.xz", content)
	hash := fmt.Sprintf("%x", sha256.Sum256(content))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/files/server/macos/stable/v10.11.11/arm64/") && r.URL.RawQuery == "mirrorlist" {
			fmt.Fprintf(w, `<tr><td>SHA256</td><td class="sum">%s</td></tr>`, hash)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	validator := testPackageValidator(server)
	var phases []PackageValidationPhase
	result, err := validator.Validate(context.Background(), path, func(phase PackageValidationPhase) { phases = append(phases, phase) })
	if err != nil {
		t.Fatal(err)
	}
	if result.LocalSHA256 != hash || result.LocalSize != int64(len(content)) || result.OfficialSHA256 != hash || !strings.Contains(result.SourceURL, "/macos/stable/") {
		t.Fatalf("validation=%+v", result)
	}
	if len(phases) != 1 || phases[0] != PackageValidationConnecting {
		t.Fatalf("phases=%v", phases)
	}
}

func TestPackageValidatorDownloadsArchiveOnlyPackage(t *testing.T) {
	content := bytes.Repeat([]byte("archive-jellyfin"), 128)
	path := writeValidatorPackage(t, "jellyfin_10.9.11-amd64.tar.gz", content)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/archive/server/linux/stable/v10.9.11/amd64/") {
			w.Header().Set("Content-Length", strconv.Itoa(len(content)))
			if r.Method != http.MethodHead {
				_, _ = w.Write(content)
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	validator := testPackageValidator(server)
	var phases []PackageValidationPhase
	result, err := validator.Validate(context.Background(), path, func(phase PackageValidationPhase) { phases = append(phases, phase) })
	if err != nil {
		t.Fatal(err)
	}
	if result.OfficialSHA256 == "" || len(phases) != 2 || phases[0] != PackageValidationConnecting || phases[1] != PackageValidationDownloading {
		t.Fatalf("result=%+v phases=%v", result, phases)
	}
}

func TestPackageValidatorRejectsChecksumMismatch(t *testing.T) {
	path := writeValidatorPackage(t, "jellyfin_10.11.11-arm64.tar.xz", []byte("tampered"))
	official := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery == "mirrorlist" {
			fmt.Fprintf(w, `<tr><td>SHA256</td><td>%s</td></tr>`, official)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	_, err := testPackageValidator(server).Validate(context.Background(), path, nil)
	if err == nil || !strings.Contains(err.Error(), ErrPackageChecksumMismatch.Error()) {
		t.Fatalf("error=%v", err)
	}
}

func TestPackageValidatorBoundsRepositoryConnection(t *testing.T) {
	path := writeValidatorPackage(t, "jellyfin_10.11.11-arm64.tar.xz", []byte("package"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	validator := testPackageValidator(server)
	validator.RequestTimeout = 20 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := validator.Validate(ctx, path, nil)
	if err == nil || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("error=%v elapsed=%s", err, time.Since(started))
	}
}

func TestPackageValidatorBoundsArchiveDownload(t *testing.T) {
	content := []byte("package-with-matching-size")
	path := writeValidatorPackage(t, "jellyfin_10.9.11-amd64.tar.xz", content)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/archive/server/linux/stable/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		if r.Method == http.MethodHead {
			return
		}
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	validator := testPackageValidator(server)
	validator.DownloadTimeout = 25 * time.Millisecond
	started := time.Now()
	_, err := validator.Validate(context.Background(), path, nil)
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("error=%v elapsed=%s", err, time.Since(started))
	}
}

func testPackageValidator(server *httptest.Server) *PackageValidator {
	return &PackageValidator{
		Client: server.Client(), FilesBase: server.URL + "/files/server", ArchiveBase: server.URL + "/archive/server",
		RequestTimeout: time.Second, DownloadTimeout: time.Second,
	}
}

func writeValidatorPackage(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
