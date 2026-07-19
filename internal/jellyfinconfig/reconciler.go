package jellyfinconfig

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

const (
	maxCustomCSSBytes   = 4 << 20
	maxSplashImageBytes = 16 << 20
)

type Result struct {
	ChangedFiles []string
	BackupFiles  []string
	SkippedSetup bool
}

type Reconciler struct {
	cfg *config.Config
}

func New(cfg *config.Config) *Reconciler { return &Reconciler{cfg: cfg} }

type elementChange struct {
	list    bool
	scalar  string
	strings []string
}

type filePlan struct {
	path     string
	original []byte
	updated  []byte
	mode     os.FileMode
	existed  bool
}

var (
	replaceConfigFile   = atomicWrite
	syncConfigDirectory = syncParentDirectory
)

func (r *Reconciler) Reconcile() (Result, error) {
	var result Result
	if !r.cfg.Jellyfin.HasManagedSettings() {
		return result, nil
	}
	systemPath := filepath.Join(r.cfg.Jellyfin.ConfigDir, "system.xml")
	systemData, err := os.ReadFile(systemPath)
	if errors.Is(err, os.ErrNotExist) {
		result.SkippedSetup = true
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("read Jellyfin system configuration: %w", err)
	}
	completed, err := scalarElement(systemData, "ServerConfiguration", "IsStartupWizardCompleted")
	if err != nil {
		return result, fmt.Errorf("inspect Jellyfin setup state: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(completed), "true") {
		result.SkippedSetup = true
		return result, nil
	}

	changes, err := r.changes()
	if err != nil {
		return result, err
	}
	roots := map[string]string{
		"system.xml":   "ServerConfiguration",
		"branding.xml": "BrandingOptions",
		"encoding.xml": "EncodingOptions",
		"network.xml":  "NetworkConfiguration",
	}
	var plans []filePlan
	for name, fileChanges := range changes {
		if len(fileChanges) == 0 {
			continue
		}
		path := filepath.Join(r.cfg.Jellyfin.ConfigDir, name)
		data, readErr := os.ReadFile(path)
		existed := readErr == nil
		if errors.Is(readErr, os.ErrNotExist) && name == "branding.xml" {
			data, readErr = newXML(roots[name], fileChanges)
		}
		if readErr != nil {
			return result, fmt.Errorf("read Jellyfin configuration %s: %w", name, readErr)
		}
		updated, changed, patchErr := patchXML(data, roots[name], fileChanges)
		if patchErr != nil {
			return result, fmt.Errorf("patch Jellyfin configuration %s: %w", name, patchErr)
		}
		if !changed && existed {
			continue
		}
		mode := os.FileMode(0o600)
		if existed {
			info, statErr := os.Stat(path)
			if statErr != nil {
				return result, fmt.Errorf("stat Jellyfin configuration %s: %w", name, statErr)
			}
			mode = info.Mode().Perm()
		}
		plans = append(plans, filePlan{path: path, original: data, updated: updated, mode: mode, existed: existed})
	}
	if len(plans) == 0 {
		return result, nil
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].path < plans[j].path })
	for _, plan := range plans {
		if !plan.existed {
			continue
		}
		backup := plan.path + ".remora.bak"
		if err := atomicWrite(backup, plan.original, plan.mode); err != nil {
			return result, fmt.Errorf("back up Jellyfin configuration %s: %w", filepath.Base(plan.path), err)
		}
		result.BackupFiles = append(result.BackupFiles, backup)
	}
	var written []filePlan
	for _, plan := range plans {
		if err := replaceConfigFile(plan.path, plan.updated, plan.mode); err != nil {
			// atomicWrite may fail after rename when the parent directory cannot
			// be synced. Include the current plan because its destination may
			// already have changed even though the write returned an error.
			rollbackPlans := append(append([]filePlan(nil), written...), plan)
			rollbackErr := rollback(rollbackPlans)
			if rollbackErr != nil {
				return result, fmt.Errorf("write Jellyfin configuration %s: %w; rollback failed: %v", filepath.Base(plan.path), err, rollbackErr)
			}
			return result, fmt.Errorf("write Jellyfin configuration %s: %w", filepath.Base(plan.path), err)
		}
		written = append(written, plan)
		result.ChangedFiles = append(result.ChangedFiles, plan.path)
	}
	return result, nil
}

func rollback(plans []filePlan) error {
	var failures []string
	for i := len(plans) - 1; i >= 0; i-- {
		plan := plans[i]
		var err error
		if plan.existed {
			err = atomicWrite(plan.path, plan.original, plan.mode)
		} else {
			err = os.Remove(plan.path)
			if errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", filepath.Base(plan.path), err))
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func (r *Reconciler) changes() (map[string]map[string]elementChange, error) {
	files := map[string]map[string]elementChange{
		"system.xml": {}, "branding.xml": {}, "encoding.xml": {}, "network.xml": {},
	}
	system := files["system.xml"]
	general := r.cfg.Jellyfin.General
	putString(system, "ServerName", general.Settings.ServerName, false)
	if err := putPath(system, "CachePath", general.Paths.CachePath); err != nil {
		return nil, fmt.Errorf("jellyfin.general.paths.cache-path: %w", err)
	}
	if err := putPath(system, "MetadataPath", general.Paths.MetadataPath); err != nil {
		return nil, fmt.Errorf("jellyfin.general.paths.metadata-path: %w", err)
	}
	putInt(system, "LibraryScanFanoutConcurrency", general.Performance.ParallelLibraryScanTasksLimit)
	putInt(system, "ParallelImageEncodingLimit", general.Performance.ParallelImageEncodingLimit)

	branding := files["branding.xml"]
	brand := r.cfg.Jellyfin.Branding
	putBool(branding, "SplashscreenEnabled", brand.EnableSplashScreen)
	if brand.SplashScreenImage.Set {
		value := optionalString(brand.SplashScreenImage, false)
		if value != "" {
			if err := validateImage(value); err != nil {
				return nil, fmt.Errorf("jellyfin.branding.splash-screen-image: %w", err)
			}
		}
		branding["SplashscreenLocation"] = elementChange{scalar: value}
	}
	putString(branding, "LoginDisclaimer", brand.LoginDisclaimer, false)
	if brand.CustomCSSCode.Set {
		value := optionalString(brand.CustomCSSCode, false)
		if value != "" {
			css, err := readCSS(value)
			if err != nil {
				return nil, fmt.Errorf("jellyfin.branding.custom-css-code: %w", err)
			}
			value = css
		}
		branding["CustomCss"] = elementChange{scalar: value}
	}

	encoding := files["encoding.xml"]
	transcoding := r.cfg.Jellyfin.Playback.Transcoding
	if err := putPath(encoding, "TranscodingTempPath", transcoding.TranscodePath); err != nil {
		return nil, fmt.Errorf("jellyfin.playback.transcoding.transcode-path: %w", err)
	}
	putBool(encoding, "EnableFallbackFont", transcoding.EnableFallbackFonts)
	if err := putPath(encoding, "FallbackFontPath", transcoding.FallbackFontFolderPath); err != nil {
		return nil, fmt.Errorf("jellyfin.playback.transcoding.fallback-font-folder-path: %w", err)
	}

	network := files["network.xml"]
	address := r.cfg.Jellyfin.Networking.ServerAddressSettings
	if address.LocalHTTPPortConfigured {
		network["InternalHttpPort"] = elementChange{scalar: strconv.Itoa(address.LocalHTTPPort)}
	}
	if address.LocalHTTPSPortConfigured {
		network["InternalHttpsPort"] = elementChange{scalar: strconv.Itoa(address.LocalHTTPSPort)}
	}
	if address.EnableHTTPSConfigured && !address.EnableHTTPSNull {
		network["EnableHttps"] = elementChange{scalar: strconv.FormatBool(address.EnableHTTPS)}
	}
	if address.BaseURLConfigured {
		value := address.BaseURL
		if address.BaseURLNull {
			value = ""
		}
		network["BaseUrl"] = elementChange{scalar: value}
	}
	if address.BindToLocalNetworkAddress.Set {
		values := address.BindToLocalNetworkAddress.Value
		if address.BindToLocalNetworkAddress.Null {
			values = nil
		}
		network["LocalNetworkAddresses"] = elementChange{list: true, strings: append([]string(nil), values...)}
	}
	putBool(network, "EnableIPv4", r.cfg.Jellyfin.Networking.IPProtocols.EnableIPv4)
	putBool(network, "EnableIPv6", r.cfg.Jellyfin.Networking.IPProtocols.EnableIPv6)
	return files, nil
}

func putString(changes map[string]elementChange, name string, value config.Optional[string], defaultSentinel bool) {
	if value.Set {
		changes[name] = elementChange{scalar: optionalString(value, defaultSentinel)}
	}
}

func putPath(changes map[string]elementChange, name string, value config.Optional[string]) error {
	if !value.Set {
		return nil
	}
	path := optionalString(value, true)
	if path != "" && !filepath.IsAbs(path) {
		return fmt.Errorf("must be an absolute path, null, or default")
	}
	changes[name] = elementChange{scalar: path}
	return nil
}

func putInt(changes map[string]elementChange, name string, value config.Optional[int]) {
	if value.Set {
		v := value.Value
		if value.Null {
			v = 0
		}
		changes[name] = elementChange{scalar: strconv.Itoa(v)}
	}
}

func putBool(changes map[string]elementChange, name string, value config.Optional[bool]) {
	if value.Set {
		v := value.Value
		if value.Null {
			v = false
		}
		changes[name] = elementChange{scalar: strconv.FormatBool(v)}
	}
}

func optionalString(value config.Optional[string], defaultSentinel bool) string {
	if value.Null || (defaultSentinel && strings.EqualFold(strings.TrimSpace(value.Value), "default")) {
		return ""
	}
	return value.Value
}

func readCSS(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("must be an absolute file path")
	}
	data, err := readBoundedAsset(path, maxCustomCSSBytes, false)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return "", errors.New("file is not valid UTF-8")
	}
	return string(data), nil
}

func validateImage(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("must be an absolute file path")
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".webp":
	default:
		return errors.New("supported image extensions are png, jpg, jpeg, and webp")
	}
	_, err := readBoundedAsset(path, maxSplashImageBytes, true)
	return err
}

func readBoundedAsset(path string, limit int64, requireContent bool) ([]byte, error) {
	pathInfo, err := lstatPathWithoutSymlinks(path)
	if err != nil {
		return nil, err
	}
	if !pathInfo.Mode().IsRegular() {
		return nil, errors.New("must reference a regular file")
	}
	if pathInfo.Size() > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		return nil, errors.New("asset changed while it was being opened")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	if requireContent && len(data) == 0 {
		return nil, errors.New("must reference a non-empty regular file")
	}
	return data, nil
}

func lstatPathWithoutSymlinks(path string) (os.FileInfo, error) {
	clean := filepath.Clean(path)
	var leaf os.FileInfo
	for current := clean; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symbolic links are not allowed: %s", current)
		}
		if current == clean {
			leaf = info
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return leaf, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".remora-xml-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if err = tmp.Chmod(mode.Perm()); err != nil {
		return err
	}
	if err = preserveOwnership(tmpPath, path); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmpPath, path); err != nil {
		return err
	}
	if err = syncConfigDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	return nil
}

func syncParentDirectory(path string) error {
	parent, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := parent.Sync(); err != nil {
		_ = parent.Close()
		return err
	}
	return parent.Close()
}

type elementSpan struct{ start, end int64 }

type xmlLayout struct {
	rootName string
	rootEnd  int64
	elements map[string]elementSpan
}

func scanXML(data []byte) (xmlLayout, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	layout := xmlLayout{elements: make(map[string]elementSpan)}
	depth := 0
	var directName string
	var directStart int64
	for {
		start := decoder.InputOffset()
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return layout, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				layout.rootName = value.Name.Local
			} else if depth == 1 {
				directName, directStart = value.Name.Local, start
			}
			depth++
		case xml.EndElement:
			if depth == 2 && directName == value.Name.Local {
				if _, duplicate := layout.elements[directName]; duplicate {
					return layout, fmt.Errorf("duplicate direct element %s", directName)
				}
				layout.elements[directName] = elementSpan{start: directStart, end: decoder.InputOffset()}
				directName = ""
			}
			if depth == 1 {
				layout.rootEnd = start
			}
			depth--
			if depth < 0 {
				return layout, errors.New("invalid XML element nesting")
			}
		}
	}
	if depth != 0 || layout.rootName == "" || layout.rootEnd == 0 {
		return layout, errors.New("incomplete XML document")
	}
	return layout, nil
}

func scalarElement(data []byte, root, name string) (string, error) {
	layout, err := scanXML(data)
	if err != nil {
		return "", err
	}
	if layout.rootName != root {
		return "", fmt.Errorf("root is %s, want %s", layout.rootName, root)
	}
	span, ok := layout.elements[name]
	if !ok {
		return "", fmt.Errorf("element %s is missing", name)
	}
	value, _, err := readElement(data[span.start:span.end], false)
	return value, err
}

func patchXML(data []byte, root string, changes map[string]elementChange) ([]byte, bool, error) {
	layout, err := scanXML(data)
	if err != nil {
		return nil, false, err
	}
	if layout.rootName != root {
		return nil, false, fmt.Errorf("root is %s, want %s", layout.rootName, root)
	}
	type replacement struct {
		start, end int64
		data       []byte
	}
	var replacements []replacement
	var missing []string
	for name, change := range changes {
		span, ok := layout.elements[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		scalar, values, readErr := readElement(data[span.start:span.end], change.list)
		if readErr != nil {
			return nil, false, fmt.Errorf("read element %s: %w", name, readErr)
		}
		matches := scalar == change.scalar
		if change.list {
			matches = stringSlicesEqual(values, change.strings)
		}
		if !matches {
			replacements = append(replacements, replacement{start: span.start, end: span.end, data: renderElement(name, change)})
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		var inserted bytes.Buffer
		for _, name := range missing {
			inserted.WriteString("  ")
			inserted.Write(renderElement(name, changes[name]))
			inserted.WriteByte('\n')
		}
		replacements = append(replacements, replacement{start: layout.rootEnd, end: layout.rootEnd, data: inserted.Bytes()})
	}
	if len(replacements) == 0 {
		return data, false, nil
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })
	out := append([]byte(nil), data...)
	for _, replacement := range replacements {
		next := make([]byte, 0, len(out)-int(replacement.end-replacement.start)+len(replacement.data))
		next = append(next, out[:replacement.start]...)
		next = append(next, replacement.data...)
		next = append(next, out[replacement.end:]...)
		out = next
	}
	if _, err := scanXML(out); err != nil {
		return nil, false, fmt.Errorf("validate patched XML: %w", err)
	}
	return out, true, nil
}

func readElement(data []byte, list bool) (string, []string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth := 0
	var scalar strings.Builder
	var item strings.Builder
	var values []string
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", nil, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			depth++
			if list && depth == 2 && value.Name.Local != "string" {
				return "", nil, fmt.Errorf("unexpected list element %s", value.Name.Local)
			}
		case xml.CharData:
			if !list && depth == 1 {
				scalar.Write(value)
			} else if list && depth == 2 {
				item.Write(value)
			}
		case xml.EndElement:
			if list && depth == 2 {
				values = append(values, item.String())
				item.Reset()
			}
			depth--
		}
	}
	return scalar.String(), values, nil
}

func renderElement(name string, change elementChange) []byte {
	var out bytes.Buffer
	out.WriteByte('<')
	out.WriteString(name)
	out.WriteByte('>')
	if change.list {
		for _, value := range change.strings {
			out.WriteString("<string>")
			_ = xml.EscapeText(&out, []byte(value))
			out.WriteString("</string>")
		}
	} else {
		_ = xml.EscapeText(&out, []byte(change.scalar))
	}
	out.WriteString("</")
	out.WriteString(name)
	out.WriteByte('>')
	return out.Bytes()
}

func newXML(root string, changes map[string]elementChange) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<")
	out.WriteString(root)
	out.WriteString(" xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\" xmlns:xsd=\"http://www.w3.org/2001/XMLSchema\">\n")
	keys := make([]string, 0, len(changes))
	for key := range changes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out.WriteString("  ")
		out.Write(renderElement(key, changes[key]))
		out.WriteByte('\n')
	}
	out.WriteString("</")
	out.WriteString(root)
	out.WriteString(">\n")
	if _, err := scanXML(out.Bytes()); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}
