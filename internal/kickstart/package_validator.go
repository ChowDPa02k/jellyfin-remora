package kickstart

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type PackageValidationPhase string

const (
	PackageValidationConnecting  PackageValidationPhase = "Connecting Jellyfin repo"
	PackageValidationDownloading PackageValidationPhase = "Downloading package"
)

const (
	defaultRepoConnectTimeout  = 5 * time.Second
	defaultRepoRequestTimeout  = 15 * time.Second
	defaultRepoDownloadTimeout = 10 * time.Minute
	maxMirrorlistBytes         = 4 << 20
)

var (
	ErrPackageNotFound         = errors.New("package was not found in the official Jellyfin repository")
	ErrPackageChecksumMismatch = errors.New("package checksum does not match the official Jellyfin repository")
	packageSHA256Row           = regexp.MustCompile(`<tr><td>SHA256</td><td[^>]*>([a-f0-9]{64})</td>`)
	unstablePackageVersion     = regexp.MustCompile(`^\d{8,10}$`)
	packageVersionPattern      = `\d+(?:\.\d+){1,2}-rc\d+|\d{8,10}|\d+\.\d+\.\d+`
	portablePackagePattern     = regexp.MustCompile(`^jellyfin_(?P<version>` + packageVersionPattern + `)-(?P<arch>[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)\.(?P<extension>tar\.gz|tar\.xz|zip)$`)
)

// PackageRepositoryError reports a repository failure that must not be
// mistaken for an absent package. StatusCode is zero for transport failures.
type PackageRepositoryError struct {
	Operation  string
	URL        string
	StatusCode int
	Err        error
}

func (e *PackageRepositoryError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("%s Jellyfin repository: HTTP %d", e.Operation, e.StatusCode)
	}
	return fmt.Sprintf("%s Jellyfin repository: %v", e.Operation, e.Err)
}

func (e *PackageRepositoryError) Unwrap() error { return e.Err }

type PackageValidation struct {
	Filename       string
	Version        string
	Architecture   string
	LocalSHA256    string
	LocalSize      int64
	OfficialSHA256 string
	SourceURL      string
}

type PackageValidator struct {
	Client          *http.Client
	FilesBase       string
	ArchiveBase     string
	RequestTimeout  time.Duration
	DownloadTimeout time.Duration
}

type packageComponents struct {
	filename, version, architecture, directoryArchitecture, category string
	platforms                                                        []string
}

type packageCandidate struct {
	hash, sourceURL string
	size            int64
}

func NewPackageValidator() *PackageValidator {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: defaultRepoConnectTimeout, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = defaultRepoConnectTimeout
	transport.ResponseHeaderTimeout = defaultRepoRequestTimeout
	transport.ExpectContinueTimeout = time.Second
	transport.IdleConnTimeout = 90 * time.Second
	return &PackageValidator{
		Client:    &http.Client{Transport: transport},
		FilesBase: "https://repo.jellyfin.org/files/server", ArchiveBase: "https://repo.jellyfin.org/archive/server",
		RequestTimeout: defaultRepoRequestTimeout, DownloadTimeout: defaultRepoDownloadTimeout,
	}
}

func ValidatePackage(ctx context.Context, path string, progress func(PackageValidationPhase)) (PackageValidation, error) {
	return NewPackageValidator().Validate(ctx, path, progress)
}

func (v *PackageValidator) Validate(ctx context.Context, archivePath string, progress func(PackageValidationPhase)) (PackageValidation, error) {
	components, err := parsePackageFilename(filepath.Base(archivePath))
	if err != nil {
		return PackageValidation{}, err
	}
	localHash, localSize, err := hashPackageFile(archivePath)
	if err != nil {
		return PackageValidation{}, fmt.Errorf("hash selected Jellyfin package: %w", err)
	}
	result := PackageValidation{Filename: components.filename, Version: components.version, Architecture: components.architecture, LocalSHA256: localHash, LocalSize: localSize}
	if progress != nil {
		progress(PackageValidationConnecting)
	}

	requestTimeout := v.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = defaultRepoRequestTimeout
	}
	downloadTimeout := v.DownloadTimeout
	if downloadTimeout <= 0 {
		downloadTimeout = defaultRepoDownloadTimeout
	}
	client := v.Client
	if client == nil {
		client = NewPackageValidator().Client
	}
	filesBase := strings.TrimRight(v.FilesBase, "/")
	archiveBase := strings.TrimRight(v.ArchiveBase, "/")
	if filesBase == "" {
		filesBase = NewPackageValidator().FilesBase
	}
	if archiveBase == "" {
		archiveBase = NewPackageValidator().ArchiveBase
	}

	var candidates []packageCandidate
	var lastNetworkError error
	var lastDownloadError error
	downloadProgressShown := false
	for _, platform := range components.platforms {
		for _, category := range components.categories() {
			url := packageURL(filesBase, platform, category, packageVersionDirectory(components.version, category), components.directoryArchitecture, components.filename) + "?mirrorlist"
			hash, _, fetchErr := fetchPackageMirrorlist(ctx, client, requestTimeout, url)
			if fetchErr != nil {
				lastNetworkError = fetchErr
				continue
			}
			if hash != "" {
				candidates = append(candidates, packageCandidate{hash: hash, sourceURL: strings.TrimSuffix(url, "?mirrorlist")})
			}
		}
	}
	if len(candidates) > 0 {
		return finishPackageValidation(result, candidates)
	}

	for _, platform := range components.platforms {
		for _, category := range components.categories() {
			url := packageURL(archiveBase, platform, category, packageVersionDirectory(components.version, category), components.directoryArchitecture, components.filename)
			size, exists, _, headErr := fetchPackageSize(ctx, client, requestTimeout, url)
			if headErr != nil {
				lastNetworkError = headErr
				continue
			}
			if !exists {
				continue
			}
			candidate := packageCandidate{size: size, sourceURL: url}
			candidates = append(candidates, candidate)
			if size != localSize {
				continue
			}
			if progress != nil && !downloadProgressShown {
				progress(PackageValidationDownloading)
				downloadProgressShown = true
			}
			hash, downloadErr := downloadPackageHash(ctx, client, downloadTimeout, url, localSize)
			if downloadErr != nil {
				lastNetworkError = downloadErr
				lastDownloadError = downloadErr
				continue
			}
			candidates[len(candidates)-1].hash = hash
			if hash == localHash {
				return finishPackageValidation(result, candidates[len(candidates)-1:])
			}
		}
	}
	if lastDownloadError != nil {
		return PackageValidation{}, fmt.Errorf("download official Jellyfin package: %w", lastDownloadError)
	}
	if len(candidates) > 0 {
		return PackageValidation{}, fmt.Errorf("%w: %s", ErrPackageChecksumMismatch, components.filename)
	}
	if err := ctx.Err(); err != nil {
		return PackageValidation{}, fmt.Errorf("connect Jellyfin repository: %w", err)
	}
	if lastNetworkError != nil {
		return PackageValidation{}, fmt.Errorf("connect Jellyfin repository: %w", lastNetworkError)
	}
	return PackageValidation{}, fmt.Errorf("%w: %s", ErrPackageNotFound, components.filename)
}

func finishPackageValidation(result PackageValidation, candidates []packageCandidate) (PackageValidation, error) {
	for _, candidate := range candidates {
		if candidate.hash == result.LocalSHA256 {
			result.OfficialSHA256 = candidate.hash
			result.SourceURL = candidate.sourceURL
			return result, nil
		}
	}
	return PackageValidation{}, fmt.Errorf("%w: %s", ErrPackageChecksumMismatch, result.Filename)
}

func parsePackageFilename(name string) (packageComponents, error) {
	match := portablePackagePattern.FindStringSubmatch(name)
	if match == nil {
		return packageComponents{}, fmt.Errorf("package filename is not recognized by the Jellyfin repository: %s", name)
	}
	value := func(group string) string { return match[portablePackagePattern.SubexpIndex(group)] }
	components := packageComponents{filename: name, version: value("version"), architecture: value("arch")}
	components.directoryArchitecture = components.architecture
	extension := value("extension")
	if extension == "zip" {
		components.platforms = []string{"windows"}
	} else {
		components.platforms = []string{"linux", "macos"}
	}
	switch {
	case strings.Contains(components.version, "-rc"):
		components.category = "preview"
	case unstablePackageVersion.MatchString(components.version):
		components.category = "unstable"
	default:
		components.category = "stable"
	}
	return components, nil
}

func (c packageComponents) categories() []string {
	result := []string{c.category}
	for _, category := range []string{"stable", "preview", "unstable"} {
		if category != c.category {
			result = append(result, category)
		}
	}
	return result
}

func packageVersionDirectory(version, category string) string {
	if category == "unstable" {
		return version
	}
	return "v" + version
}

func packageURL(base, platform, category, version, architecture, filename string) string {
	return fmt.Sprintf("%s/%s/%s/%s/%s/%s", base, platform, category, version, architecture, filename)
}

func fetchPackageMirrorlist(ctx context.Context, client *http.Client, timeout time.Duration, url string) (string, bool, error) {
	response, err := packageRequest(ctx, client, timeout, http.MethodGet, url)
	if err != nil {
		return "", false, &PackageRepositoryError{Operation: "fetch package metadata from", URL: url, Err: err}
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return "", true, &PackageRepositoryError{Operation: "fetch package metadata from", URL: url, StatusCode: response.StatusCode}
	}
	if response.StatusCode >= 400 {
		return "", true, nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxMirrorlistBytes+1))
	if err != nil {
		return "", true, err
	}
	if len(body) > maxMirrorlistBytes {
		return "", true, errors.New("Jellyfin mirrorlist exceeds 4 MiB")
	}
	match := packageSHA256Row.FindSubmatch(body)
	if len(match) < 2 {
		return "", true, nil
	}
	return string(match[1]), true, nil
}

func fetchPackageSize(ctx context.Context, client *http.Client, timeout time.Duration, url string) (int64, bool, bool, error) {
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		response, err := packageRequest(ctx, client, timeout, method, url)
		if err != nil {
			return 0, false, false, &PackageRepositoryError{Operation: "inspect package at", URL: url, Err: err}
		}
		response.Body.Close()
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			return 0, false, true, &PackageRepositoryError{Operation: "inspect package at", URL: url, StatusCode: response.StatusCode}
		}
		if response.StatusCode >= 400 {
			if method == http.MethodHead && (response.StatusCode == http.StatusMethodNotAllowed || response.StatusCode == http.StatusNotImplemented) {
				continue
			}
			return 0, false, true, nil
		}
		if length := response.Header.Get("Content-Length"); length != "" {
			size, parseErr := strconv.ParseInt(length, 10, 64)
			if parseErr == nil && size >= 0 {
				return size, true, true, nil
			}
		}
	}
	return 0, false, true, nil
}

func downloadPackageHash(ctx context.Context, client *http.Client, timeout time.Duration, url string, expectedSize int64) (string, error) {
	response, err := packageRequest(ctx, client, timeout, http.MethodGet, url)
	if err != nil {
		return "", &PackageRepositoryError{Operation: "download package from", URL: url, Err: err}
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return "", &PackageRepositoryError{Operation: "download package from", URL: url, StatusCode: response.StatusCode}
	}
	if response.StatusCode >= 400 {
		return "", fmt.Errorf("download official package: HTTP %d", response.StatusCode)
	}
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(response.Body, expectedSize+1))
	if err != nil {
		return "", err
	}
	if written != expectedSize {
		return "", fmt.Errorf("official package size changed during download: got %d, want %d", written, expectedSize)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func packageRequest(parent context.Context, client *http.Client, timeout time.Duration, method, url string) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	request, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	request.Header.Set("User-Agent", "jellyfin-remora-kickstart/0.9")
	response, err := client.Do(request)
	if err != nil {
		cancel()
		return nil, err
	}
	response.Body = &cancelOnCloseReader{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

type cancelOnCloseReader struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r *cancelOnCloseReader) Close() error {
	err := r.ReadCloser.Close()
	r.cancel()
	return err
}

func hashPackageFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}
