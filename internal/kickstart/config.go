package kickstart

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
	"gopkg.in/yaml.v3"
)

const watchdogPasswordLength = 16

type Answers struct {
	Installation     Installation `yaml:"-"`
	Home             string       `yaml:"home"`
	MediaPaths       []string     `yaml:"media-paths"`
	ServerName       string       `yaml:"server-name"`
	DisplayLanguage  string       `yaml:"display-language"`
	MetadataLanguage string       `yaml:"metadata-language"`
	MetadataRegion   string       `yaml:"metadata-region"`
	AdminPassword    string       `yaml:"admin-password"`
	RunAsUser        string       `yaml:"run-as-user,omitempty"`
	RunAsGroup       string       `yaml:"run-as-group,omitempty"`
}

type mountProvider interface {
	Mounts(context.Context) ([]platform.MountInfo, error)
}

func InferStorage(ctx context.Context, backend mountProvider, home string, mediaPaths []string) ([]config.DiskConfig, error) {
	mounts, err := backend.Mounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("enumerate mounted storage: %w", err)
	}
	type requestedPath struct {
		path       string
		permission string
	}
	requests := []requestedPath{{path: home, permission: "rw"}}
	for _, path := range mediaPaths {
		requests = append(requests, requestedPath{path: path, permission: "r"})
	}

	byTarget := make(map[string]config.DiskConfig)
	for _, request := range requests {
		absolute, err := filepath.Abs(strings.TrimSpace(request.path))
		if err != nil || strings.TrimSpace(request.path) == "" {
			return nil, fmt.Errorf("invalid storage path %q", request.path)
		}
		mount, ok := containingMount(absolute, mounts)
		if !ok {
			return nil, fmt.Errorf("no mounted filesystem contains %s", absolute)
		}
		diskType := filesystemType(mount.FSType)
		if diskType == "" {
			return nil, fmt.Errorf("unsupported filesystem %q contains %s", mount.FSType, absolute)
		}
		disk := config.DiskConfig{
			Type: diskType, Device: mount.Source,
			Target: mount.Target, ProbePath: existingProbePath(absolute, mount.Target), Permission: request.permission,
			Heartbeat: heartbeatForDisk(diskType), FailureThreshold: 1,
		}
		key := filepath.Clean(mount.Target)
		if existing, found := byTarget[key]; found {
			if request.permission == "rw" {
				existing.Permission = "rw"
				existing.ProbePath = absolute
			}
			byTarget[key] = existing
			continue
		}
		byTarget[key] = disk
	}
	result := make([]config.DiskConfig, 0, len(byTarget))
	for _, disk := range byTarget {
		result = append(result, disk)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Target < result[j].Target })
	return result, nil
}

func existingProbePath(path, mountTarget string) string {
	candidate := filepath.Clean(path)
	target := filepath.Clean(mountTarget)
	for {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		if candidate == target {
			return target
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return target
		}
		relative, err := filepath.Rel(target, parent)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return target
		}
		candidate = parent
	}
}

func containingMount(path string, mounts []platform.MountInfo) (platform.MountInfo, bool) {
	var selected platform.MountInfo
	best := -1
	for _, mount := range mounts {
		target := filepath.Clean(mount.Target)
		relative, err := filepath.Rel(target, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		if len(target) > best {
			selected, best = mount, len(target)
		}
	}
	return selected, best >= 0
}

func filesystemType(fs string) string {
	switch strings.ToLower(fs) {
	case "smb", "smbfs", "cifs", "smb3":
		return "smb"
	case "nfs", "nfs4":
		return "nfs"
	case "", "autofs", "devfs", "proc", "procfs", "sysfs", "tmpfs", "devtmpfs", "overlay":
		return ""
	default:
		return "physical"
	}
}

func heartbeatForDisk(kind string) int {
	if kind == "physical" {
		return 1
	}
	return 3
}

func RandomWatchdogPassword() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	raw := make([]byte, watchdogPasswordLength)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate watchdog password: %w", err)
	}
	for index := range raw {
		raw[index] = alphabet[int(raw[index])%len(alphabet)]
	}
	return string(raw), nil
}

func BuildConfiguration(answers Answers, disks []config.DiskConfig, watchdogPassword string) ([]byte, error) {
	if err := validateAnswers(answers); err != nil {
		return nil, err
	}
	if len(watchdogPassword) != watchdogPasswordLength {
		return nil, fmt.Errorf("watchdog password must contain %d characters", watchdogPasswordLength)
	}
	home, err := filepath.Abs(answers.Home)
	if err != nil {
		return nil, err
	}
	paths := map[string]string{
		"data": filepath.Join(home, "data"), "config": filepath.Join(home, "config"),
		"cache": filepath.Join(home, "cache"), "logs": filepath.Join(home, "logs"),
		"transcode": filepath.Join(home, "transcode"),
	}

	root := map[string]any{
		"config-version": 2,
		"restapi":        map[string]any{"listen": "127.0.0.1", "port": 8095, "tcp-enabled": false, platformControlKey(): platformControlValue()},
		"remora": map[string]any{
			"server-start-timeout": "10m", "server-stop-timeout": "1m", "io-timeout": "5s", "recovery-successes": 2,
			"data-dir": filepath.Join(home, ".remora"),
			"logs":     map[string]any{"path": filepath.Join(paths["logs"], "remora"), "level": "info", "rotation-time": "24h", "rotation-size-mb": 100, "preserve-time": "168h"},
			"monitoring": map[string]any{
				"interval":     "1s",
				"jellyfin-api": map[string]any{"interval": "10s", "failure-threshold": 3},
				"user-login":   map[string]any{"enabled": true, "interval": "60s", "user": "remora", "password": watchdogPassword},
			},
		},
		"disk": diskMappings(disks),
		"jellyfin": map[string]any{
			"path": answers.Installation.Executable, "run-as-user": answers.RunAsUser, "run-as-group": answers.RunAsGroup,
			"data-dir": paths["data"], "config-dir": paths["config"], "cache-dir": paths["cache"], "log-dir": paths["logs"],
			"branding": map[string]any{"login-disclaimer": "Powered by Jellyfin Remora"},
			"playback": map[string]any{"transcoding": map[string]any{"transcode-path": paths["transcode"]}},
		},
		"init": map[string]any{
			"server-name": answers.ServerName, "display-language": answers.DisplayLanguage,
			"user": "admin", "password": answers.AdminPassword,
			"preferred-metadata-language": answers.MetadataLanguage,
			"preferred-metadata-region":   answers.MetadataRegion,
			"allow-remote-connections":    true,
		},
	}
	if answers.Installation.WebDir != "" {
		root["jellyfin"].(map[string]any)["web-dir"] = answers.Installation.WebDir
	}
	return yaml.Marshal(root)
}

func diskMappings(disks []config.DiskConfig) []map[string]any {
	result := make([]map[string]any, 0, len(disks))
	for _, disk := range disks {
		entry := map[string]any{
			"type": disk.Type, "target": disk.Target, "probe-path": disk.ProbePath,
			"permission": disk.Permission, "heartbeat": disk.Heartbeat,
			"failure-threshold": disk.FailureThreshold,
		}
		for key, value := range map[string]string{
			"device": disk.Device, "uuid": disk.UUID, "volume-guid": disk.VolumeGUID,
			"volume-label": disk.VolumeLabel, "filesystem": disk.Filesystem,
			"options": disk.Options, "user": disk.User, "password": disk.Password,
			"credential": disk.Credential,
		} {
			if value != "" {
				entry[key] = value
			}
		}
		result = append(result, entry)
	}
	return result
}

func validateAnswers(answers Answers) error {
	for name, value := range map[string]string{
		"Jellyfin executable": answers.Installation.Executable, "Jellyfin home": answers.Home,
		"server name": answers.ServerName, "display language": answers.DisplayLanguage,
		"metadata language": answers.MetadataLanguage, "metadata region": answers.MetadataRegion,
		"administrator password": answers.AdminPassword,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	catalog, err := Localizations()
	if err != nil {
		return err
	}
	for name, selected := range map[string]struct {
		value   string
		options []string
	}{
		"display language":  {answers.DisplayLanguage, catalog.DisplayLanguages},
		"metadata language": {answers.MetadataLanguage, catalog.MetadataLanguages},
		"metadata region":   {answers.MetadataRegion, catalog.MetadataRegions},
	} {
		if !contains(selected.options, selected.value) {
			return fmt.Errorf("%s %q is not a Jellyfin selection label", name, selected.value)
		}
	}
	return nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func platformControlKey() string {
	if runtime.GOOS == "windows" {
		return "named-pipe"
	}
	return "unix-socket"
}

func platformControlValue() string {
	if runtime.GOOS == "windows" {
		return `\\.\pipe\jellyfin-remora`
	}
	return "/tmp/.s.remora.8095"
}

func ValidateGeneratedConfiguration(data []byte) error {
	_, err := config.Parse(data)
	if err != nil {
		return fmt.Errorf("generated configuration is invalid: %w", err)
	}
	return nil
}
