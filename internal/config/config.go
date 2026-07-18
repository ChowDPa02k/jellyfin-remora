package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration struct{ time.Duration }

type Optional[T any] struct {
	Set   bool
	Null  bool
	Value T
}

func (o *Optional[T]) UnmarshalYAML(n *yaml.Node) error {
	return decodeOptional(n, o)
}

type OptionalStrings struct {
	Set   bool
	Null  bool
	Value []string
}

func decodeOptional[T any](n *yaml.Node, out *Optional[T]) error {
	out.Set = true
	if n.Tag == "!!null" {
		out.Null = true
		var zero T
		out.Value = zero
		return nil
	}
	return n.Decode(&out.Value)
}

func decodeOptionalMapping(n *yaml.Node, fields map[string]func(*yaml.Node) error) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("expected a mapping, got %s", n.ShortTag())
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		name := n.Content[i].Value
		decode, ok := fields[name]
		if !ok {
			return fmt.Errorf("unknown field %q", name)
		}
		if err := decode(n.Content[i+1]); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func (o *OptionalStrings) UnmarshalYAML(n *yaml.Node) error {
	o.Set = true
	if n.Tag == "!!null" {
		o.Null = true
		o.Value = nil
		return nil
	}
	if n.Kind == yaml.ScalarNode {
		var value string
		if err := n.Decode(&value); err != nil {
			return err
		}
		o.Value = []string{value}
		return nil
	}
	return n.Decode(&o.Value)
}

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!int" {
		var seconds int64
		if err := n.Decode(&seconds); err != nil {
			return err
		}
		d.Duration = time.Duration(seconds) * time.Second
		return nil
	}
	value := n.Value
	if strings.HasSuffix(value, "d") || strings.HasSuffix(value, "w") {
		unit := 24 * time.Hour
		if strings.HasSuffix(value, "w") {
			unit = 7 * 24 * time.Hour
		}
		number, err := strconv.ParseFloat(value[:len(value)-1], 64)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", value, err)
		}
		d.Duration = time.Duration(number * float64(unit))
		return nil
	}
	v, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", n.Value, err)
	}
	d.Duration = v
	return nil
}

type Config struct {
	ConfigVersion int             `yaml:"config-version"`
	LegacyConfig  bool            `yaml:"-"`
	Migrations    MigrationReport `yaml:"-"`
	RESTAPI       RESTAPIConfig   `yaml:"restapi"`
	Remora        RemoraConfig    `yaml:"remora"`
	Disks         []DiskConfig    `yaml:"disk"`
	Jellyfin      JellyfinConfig  `yaml:"jellyfin"`
	Init          InitConfig      `yaml:"init,omitempty"`
}

type RESTAPIConfig struct {
	Listen     string `yaml:"listen"`
	Port       int    `yaml:"port"`
	UnixSocket string `yaml:"unix-socket"`
	NamedPipe  string `yaml:"named-pipe"`
}

type RemoraConfig struct {
	ServerStartTimeout  Duration                `yaml:"server-start-timeout"`
	ServerStopTimeout   Duration                `yaml:"server-stop-timeout"`
	IOTimeout           Duration                `yaml:"io-timeout"`
	RecoverySuccesses   int                     `yaml:"recovery-successes"`
	Monitoring          MonitoringConfig        `yaml:"monitoring"`
	DataDir             string                  `yaml:"data-dir"`
	Logs                LogConfig               `yaml:"logs"`
	HeartbeatInterval   Duration                `yaml:"-"`
	HealthAPIHeartbeat  int                     `yaml:"-"`
	APIFailureThreshold int                     `yaml:"-"`
	UserLoginWatchdog   UserLoginWatchdogConfig `yaml:"-"`
}

type MonitoringConfig struct {
	Interval    Duration                 `yaml:"interval"`
	JellyfinAPI JellyfinAPIMonitorConfig `yaml:"jellyfin-api"`
	Database    DatabaseMonitorConfig    `yaml:"database,omitempty"`
	UserLogin   UserLoginWatchdogConfig  `yaml:"user-login,omitempty"`
}

type JellyfinAPIMonitorConfig struct {
	Interval         Duration `yaml:"interval"`
	FailureThreshold int      `yaml:"failure-threshold"`
}

type DatabaseMonitorConfig struct {
	Enabled            *bool    `yaml:"enabled,omitempty"`
	ConfirmationWindow Duration `yaml:"confirmation-window"`
	FailureThreshold   int      `yaml:"failure-threshold"`
}

func (c DatabaseMonitorConfig) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

type UserLoginWatchdogConfig struct {
	Enabled   bool     `yaml:"enabled"`
	Interval  Duration `yaml:"interval"`
	User      string   `yaml:"user"`
	Password  string   `yaml:"password"`
	Heartbeat int      `yaml:"-"`
}

type InitConfig struct {
	ServerName                string `yaml:"server-name"`
	DisplayLanguage           string `yaml:"display-language"`
	User                      string `yaml:"user"`
	Password                  string `yaml:"password"`
	PreferredMetadataLanguage string `yaml:"preferred-metadata-language"`
	PreferredMetadataRegion   string `yaml:"preferred-metadata-region"`
	AllowRemoteConnections    bool   `yaml:"allow-remote-connections"`
}

type LogConfig struct {
	Path           string   `yaml:"path"`
	Level          string   `yaml:"level"`
	RotationTime   Duration `yaml:"rotation-time"`
	RotationSizeMB int64    `yaml:"rotation-size-mb"`
	PreserveTime   Duration `yaml:"preserve-time"`
}

type DiskConfig struct {
	Type             string `yaml:"type"`
	Device           string `yaml:"device"`
	UUID             string `yaml:"uuid"`
	VolumeGUID       string `yaml:"volume-guid"`
	VolumeLabel      string `yaml:"volume-label"`
	Filesystem       string `yaml:"filesystem"`
	Options          string `yaml:"options"`
	User             string `yaml:"user"`
	Password         string `yaml:"password"`
	Credential       string `yaml:"credential"`
	Target           string `yaml:"target"`
	ProbePath        string `yaml:"probe-path"`
	Permission       string `yaml:"permission"`
	Heartbeat        int    `yaml:"heartbeat"`
	Hearbeat         int    `yaml:"hearbeat,omitempty"`
	FailureThreshold int    `yaml:"failure-threshold"`
}

type JellyfinConfig struct {
	Path       string            `yaml:"path"`
	RunAsUser  string            `yaml:"run-as-user"`
	RunAsGroup string            `yaml:"run-as-group"`
	DataDir    string            `yaml:"data-dir"`
	ConfigDir  string            `yaml:"config-dir"`
	CacheDir   string            `yaml:"cache-dir"`
	LogDir     string            `yaml:"log-dir"`
	WebDir     string            `yaml:"web-dir"`
	Env        map[string]string `yaml:"env,omitempty"`
	Parameters map[string]any    `yaml:"parameters"`
	General    GeneralConfig     `yaml:"general,omitempty"`
	Branding   BrandingConfig    `yaml:"branding,omitempty"`
	Playback   PlaybackConfig    `yaml:"playback,omitempty"`
	Networking NetworkingConfig  `yaml:"networking,omitempty"`
}

type GeneralConfig struct {
	Settings    GeneralSettings    `yaml:"settings,omitempty"`
	Paths       GeneralPaths       `yaml:"paths,omitempty"`
	Performance GeneralPerformance `yaml:"performance,omitempty"`
}

type GeneralSettings struct {
	ServerName Optional[string] `yaml:"server-name,omitempty"`
}

func (c *GeneralSettings) UnmarshalYAML(n *yaml.Node) error {
	return decodeOptionalMapping(n, map[string]func(*yaml.Node) error{
		"server-name": func(v *yaml.Node) error { return decodeOptional(v, &c.ServerName) },
	})
}

type GeneralPaths struct {
	CachePath    Optional[string] `yaml:"cache-path,omitempty"`
	MetadataPath Optional[string] `yaml:"metadata-path,omitempty"`
}

func (c *GeneralPaths) UnmarshalYAML(n *yaml.Node) error {
	return decodeOptionalMapping(n, map[string]func(*yaml.Node) error{
		"cache-path":    func(v *yaml.Node) error { return decodeOptional(v, &c.CachePath) },
		"metadata-path": func(v *yaml.Node) error { return decodeOptional(v, &c.MetadataPath) },
	})
}

type GeneralPerformance struct {
	ParallelLibraryScanTasksLimit Optional[int] `yaml:"parallel-library-scan-tasks-limit,omitempty"`
	ParallelImageEncodingLimit    Optional[int] `yaml:"parallel-image-encoding-limit,omitempty"`
}

func (c *GeneralPerformance) UnmarshalYAML(n *yaml.Node) error {
	return decodeOptionalMapping(n, map[string]func(*yaml.Node) error{
		"parallel-library-scan-tasks-limit": func(v *yaml.Node) error {
			return decodeOptional(v, &c.ParallelLibraryScanTasksLimit)
		},
		"parallel-image-encoding-limit": func(v *yaml.Node) error {
			return decodeOptional(v, &c.ParallelImageEncodingLimit)
		},
	})
}

type BrandingConfig struct {
	EnableSplashScreen Optional[bool]   `yaml:"enable-splash-screen,omitempty"`
	SplashScreenImage  Optional[string] `yaml:"splash-screen-image,omitempty"`
	LoginDisclaimer    Optional[string] `yaml:"login-disclaimer,omitempty"`
	CustomCSSCode      Optional[string] `yaml:"custom-css-code,omitempty"`
}

func (c *BrandingConfig) UnmarshalYAML(n *yaml.Node) error {
	return decodeOptionalMapping(n, map[string]func(*yaml.Node) error{
		"enable-splash-screen": func(v *yaml.Node) error { return decodeOptional(v, &c.EnableSplashScreen) },
		"splash-screen-image":  func(v *yaml.Node) error { return decodeOptional(v, &c.SplashScreenImage) },
		"login-disclaimer":     func(v *yaml.Node) error { return decodeOptional(v, &c.LoginDisclaimer) },
		"custom-css-code":      func(v *yaml.Node) error { return decodeOptional(v, &c.CustomCSSCode) },
	})
}

type PlaybackConfig struct {
	Transcoding TranscodingConfig `yaml:"transcoding,omitempty"`
}

type TranscodingConfig struct {
	TranscodePath          Optional[string] `yaml:"transcode-path,omitempty"`
	EnableFallbackFonts    Optional[bool]   `yaml:"enable-fallback-fonts,omitempty"`
	FallbackFontFolderPath Optional[string] `yaml:"fallback-font-folder-path,omitempty"`
}

func (c *TranscodingConfig) UnmarshalYAML(n *yaml.Node) error {
	return decodeOptionalMapping(n, map[string]func(*yaml.Node) error{
		"transcode-path":            func(v *yaml.Node) error { return decodeOptional(v, &c.TranscodePath) },
		"enable-fallback-fonts":     func(v *yaml.Node) error { return decodeOptional(v, &c.EnableFallbackFonts) },
		"fallback-font-folder-path": func(v *yaml.Node) error { return decodeOptional(v, &c.FallbackFontFolderPath) },
	})
}

type NetworkingConfig struct {
	ServerAddressSettings ServerAddressSettings `yaml:"server-address-settings"`
	IPProtocols           IPProtocols           `yaml:"ip-protocols,omitempty"`
}

type ServerAddressSettings struct {
	LocalHTTPPort             int             `yaml:"-"`
	LocalHTTPSPort            int             `yaml:"-"`
	EnableHTTPS               bool            `yaml:"-"`
	BaseURL                   string          `yaml:"-"`
	LocalHTTPPortConfigured   bool            `yaml:"-"`
	LocalHTTPSPortConfigured  bool            `yaml:"-"`
	EnableHTTPSConfigured     bool            `yaml:"-"`
	BaseURLConfigured         bool            `yaml:"-"`
	BaseURLNull               bool            `yaml:"-"`
	BindToLocalNetworkAddress OptionalStrings `yaml:"bind-to-local-network-address,omitempty"`
}

func (s *ServerAddressSettings) UnmarshalYAML(n *yaml.Node) error {
	return decodeOptionalMapping(n, map[string]func(*yaml.Node) error{
		"local-http-port-number": func(v *yaml.Node) error {
			s.LocalHTTPPortConfigured = true
			return v.Decode(&s.LocalHTTPPort)
		},
		"local-https-port-number": func(v *yaml.Node) error {
			s.LocalHTTPSPortConfigured = true
			return v.Decode(&s.LocalHTTPSPort)
		},
		"enable-https": func(v *yaml.Node) error {
			s.EnableHTTPSConfigured = true
			return v.Decode(&s.EnableHTTPS)
		},
		"base-url": func(v *yaml.Node) error {
			var value Optional[string]
			if err := decodeOptional(v, &value); err != nil {
				return err
			}
			s.BaseURL, s.BaseURLConfigured, s.BaseURLNull = value.Value, true, value.Null
			return nil
		},
		"bind-to-local-network-address": func(v *yaml.Node) error {
			return s.BindToLocalNetworkAddress.UnmarshalYAML(v)
		},
	})
}

type IPProtocols struct {
	EnableIPv4 Optional[bool] `yaml:"enable-ipv4,omitempty"`
	EnableIPv6 Optional[bool] `yaml:"enable-ipv6,omitempty"`
}

func (c *IPProtocols) UnmarshalYAML(n *yaml.Node) error {
	return decodeOptionalMapping(n, map[string]func(*yaml.Node) error{
		"enable-ipv4": func(v *yaml.Node) error { return decodeOptional(v, &c.EnableIPv4) },
		"enable-ipv6": func(v *yaml.Node) error { return decodeOptional(v, &c.EnableIPv6) },
	})
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(b)
}

// Parse migrates, strictly decodes, defaults, and validates one configuration.
// Keeping parsing independent of filesystem I/O makes every accepted byte
// sequence subject to the same validation path in fuzz and production use.
func Parse(b []byte) (*Config, error) {
	migrated, report, err := Migrate(b)
	if err != nil {
		return nil, err
	}
	var c Config
	decoder := yaml.NewDecoder(bytes.NewReader(migrated))
	decoder.KnownFields(true)
	if err := decoder.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.Migrations = report
	c.LegacyConfig = report.FromVersion == 0
	c.defaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) defaults() {
	if c.ConfigVersion == 0 {
		c.ConfigVersion = CurrentVersion
		c.LegacyConfig = true
	}
	if c.RESTAPI.Listen == "" {
		c.RESTAPI.Listen = "127.0.0.1"
	}
	if c.RESTAPI.Port == 0 {
		c.RESTAPI.Port = 8095
	}
	defaultPlatformControl(&c.RESTAPI)
	if c.Remora.ServerStartTimeout.Duration == 0 {
		c.Remora.ServerStartTimeout.Duration = 30 * time.Second
	}
	if c.Remora.ServerStopTimeout.Duration == 0 {
		c.Remora.ServerStopTimeout.Duration = 300 * time.Second
	}
	if c.Remora.Monitoring.Interval.Duration == 0 {
		c.Remora.Monitoring.Interval.Duration = time.Second
	}
	if c.Remora.Monitoring.JellyfinAPI.Interval.Duration == 0 {
		c.Remora.Monitoring.JellyfinAPI.Interval.Duration = 10 * time.Second
	}
	if c.Remora.Monitoring.JellyfinAPI.FailureThreshold == 0 {
		c.Remora.Monitoring.JellyfinAPI.FailureThreshold = 3
	}
	if c.Remora.Monitoring.Database.ConfirmationWindow.Duration == 0 {
		c.Remora.Monitoring.Database.ConfirmationWindow.Duration = 5 * time.Minute
	}
	if c.Remora.Monitoring.Database.FailureThreshold == 0 {
		c.Remora.Monitoring.Database.FailureThreshold = 1
	}
	if c.Remora.Monitoring.UserLogin.Enabled && c.Remora.Monitoring.UserLogin.Interval.Duration == 0 {
		c.Remora.Monitoring.UserLogin.Interval.Duration = 60 * time.Second
	}
	c.Remora.HeartbeatInterval = c.Remora.Monitoring.Interval
	c.Remora.HealthAPIHeartbeat = durationTicks(c.Remora.Monitoring.JellyfinAPI.Interval.Duration, c.Remora.HeartbeatInterval.Duration)
	c.Remora.APIFailureThreshold = c.Remora.Monitoring.JellyfinAPI.FailureThreshold
	c.Remora.Monitoring.UserLogin.Heartbeat = durationTicks(c.Remora.Monitoring.UserLogin.Interval.Duration, c.Remora.HeartbeatInterval.Duration)
	c.Remora.UserLoginWatchdog = c.Remora.Monitoring.UserLogin
	if c.Remora.IOTimeout.Duration == 0 {
		c.Remora.IOTimeout.Duration = 5 * time.Second
	}
	if c.Remora.RecoverySuccesses == 0 {
		c.Remora.RecoverySuccesses = 3
	}
	if c.Remora.DataDir == "" || c.Remora.DataDir == "default" {
		c.Remora.DataDir = c.Jellyfin.DataDir
	}
	if c.Remora.Logs.Path == "" {
		c.Remora.Logs.Path = "log"
	}
	if !filepath.IsAbs(c.Remora.Logs.Path) {
		c.Remora.Logs.Path = filepath.Join(c.Remora.DataDir, c.Remora.Logs.Path)
	}
	if c.Remora.Logs.Level == "" {
		c.Remora.Logs.Level = "info"
	}
	if c.Remora.Logs.RotationTime.Duration == 0 {
		c.Remora.Logs.RotationTime.Duration = 24 * time.Hour
	}
	if c.Remora.Logs.RotationSizeMB == 0 {
		c.Remora.Logs.RotationSizeMB = 30
	}
	if c.Remora.Logs.PreserveTime.Duration == 0 {
		c.Remora.Logs.PreserveTime.Duration = 7 * 24 * time.Hour
	}
	if c.Jellyfin.Networking.ServerAddressSettings.LocalHTTPPort == 0 {
		c.Jellyfin.Networking.ServerAddressSettings.LocalHTTPPort = 8096
	}
	for i := range c.Disks {
		c.Disks[i].Type = strings.ToLower(c.Disks[i].Type)
		if c.Disks[i].Permission == "" {
			c.Disks[i].Permission = "rw"
		}
		if c.Disks[i].Heartbeat == 0 {
			c.Disks[i].Heartbeat = c.Disks[i].Hearbeat
		}
		if c.Disks[i].FailureThreshold == 0 {
			c.Disks[i].FailureThreshold = 1
		}
		if c.Disks[i].Heartbeat == 0 {
			if c.Disks[i].Type == "physical" {
				c.Disks[i].Heartbeat = 1
			} else {
				c.Disks[i].Heartbeat = 3
			}
		}
	}
}

func (c *Config) Validate() error {
	if c.ConfigVersion != CurrentVersion {
		return fmt.Errorf("unsupported config-version %d", c.ConfigVersion)
	}
	if ip := net.ParseIP(c.RESTAPI.Listen); ip == nil || !ip.IsLoopback() {
		return errors.New("restapi.listen must be a loopback IP address")
	}
	if c.RESTAPI.Port < 1 || c.RESTAPI.Port > 65535 {
		return errors.New("restapi.port must be between 1 and 65535")
	}
	if c.Remora.HeartbeatInterval.Duration <= 0 || c.Remora.Monitoring.JellyfinAPI.Interval.Duration <= 0 || c.Remora.ServerStartTimeout.Duration <= 0 || c.Remora.ServerStopTimeout.Duration <= 0 || c.Remora.IOTimeout.Duration <= 0 {
		return errors.New("remora intervals and timeouts must be positive")
	}
	if c.Remora.Monitoring.Database.ConfirmationWindow.Duration <= 0 || c.Remora.Monitoring.Database.FailureThreshold < 1 {
		return errors.New("remora.monitoring.database confirmation-window and failure-threshold must be positive")
	}
	switch c.Remora.Logs.Level {
	case "debug", "info", "warning", "warn", "error":
	default:
		return fmt.Errorf("remora.logs.level %q is invalid", c.Remora.Logs.Level)
	}
	if c.Jellyfin.Path == "" {
		return errors.New("jellyfin.path is required")
	}
	for name, p := range map[string]string{"data-dir": c.Jellyfin.DataDir, "config-dir": c.Jellyfin.ConfigDir, "cache-dir": c.Jellyfin.CacheDir, "log-dir": c.Jellyfin.LogDir} {
		if p == "" || !filepath.IsAbs(p) {
			return fmt.Errorf("jellyfin.%s must be an absolute path", name)
		}
	}
	if os.Geteuid() == 0 && c.Jellyfin.RunAsUser == "" {
		return errors.New("jellyfin.run-as-user is required when jellyfin-remora runs as root")
	}
	for name, value := range c.Jellyfin.Env {
		if name == "" || strings.ContainsAny(name, "=\x00") {
			return fmt.Errorf("jellyfin.env contains invalid variable name %q", name)
		}
		if strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("jellyfin.env.%s contains a NUL byte", name)
		}
	}
	for i, d := range c.Disks {
		c.Disks[i].Type = strings.ToLower(d.Type)
		d = c.Disks[i]
		if d.Type != "physical" && d.Type != "smb" && d.Type != "nfs" {
			return fmt.Errorf("disk[%d].type must be physical, smb, or nfs", i)
		}
		if d.Target == "" || !filepath.IsAbs(d.Target) {
			return fmt.Errorf("disk[%d].target must be absolute", i)
		}
		if d.ProbePath != "" {
			if !filepath.IsAbs(d.ProbePath) {
				return fmt.Errorf("disk[%d].probe-path must be absolute", i)
			}
			rel, err := filepath.Rel(d.Target, d.ProbePath)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("disk[%d].probe-path must be beneath target", i)
			}
		}
		if d.Permission != "r" && d.Permission != "rw" {
			return fmt.Errorf("disk[%d].permission must be r or rw", i)
		}
		if d.FailureThreshold < 1 {
			return fmt.Errorf("disk[%d].failure-threshold must be positive", i)
		}
		if d.Type == "physical" {
			identities := 0
			for _, identity := range []string{d.Device, d.UUID, d.VolumeGUID} {
				if identity != "" {
					identities++
				}
			}
			if identities != 1 {
				return fmt.Errorf("disk[%d] physical disk requires exactly one of device, uuid, or volume-guid", i)
			}
		}
		if d.Type != "physical" && d.Device == "" {
			return fmt.Errorf("disk[%d].device is required", i)
		}
		if d.Type != "physical" && (d.VolumeGUID != "" || d.VolumeLabel != "" || d.Filesystem != "") {
			return fmt.Errorf("disk[%d] volume-guid, volume-label, and filesystem are only valid for physical disks", i)
		}
		if d.Type != "smb" && d.Credential != "" {
			return fmt.Errorf("disk[%d].credential is only valid for SMB disks", i)
		}
		if err := validatePlatformDisk(i, d); err != nil {
			return err
		}
	}
	if err := validatePlatformConfig(c); err != nil {
		return err
	}
	network := c.Jellyfin.Networking.ServerAddressSettings
	if network.LocalHTTPPortConfigured && (network.LocalHTTPPort < 1 || network.LocalHTTPPort > 65535) {
		return errors.New("jellyfin.networking.server-address-settings.local-http-port-number must be between 1 and 65535")
	}
	if network.LocalHTTPSPortConfigured && (network.LocalHTTPSPort < 1 || network.LocalHTTPSPort > 65535) {
		return errors.New("jellyfin.networking.server-address-settings.local-https-port-number must be between 1 and 65535")
	}
	if value := c.Jellyfin.General.Performance.ParallelLibraryScanTasksLimit; value.Set && !value.Null && value.Value < 0 {
		return errors.New("jellyfin.general.performance.parallel-library-scan-tasks-limit cannot be negative")
	}
	if value := c.Jellyfin.General.Performance.ParallelImageEncodingLimit; value.Set && !value.Null && value.Value < 0 {
		return errors.New("jellyfin.general.performance.parallel-image-encoding-limit cannot be negative")
	}
	initConfigured := c.Init.User != "" || c.Init.Password != "" || c.Init.ServerName != "" || c.Init.DisplayLanguage != "" || c.Init.PreferredMetadataLanguage != "" || c.Init.PreferredMetadataRegion != ""
	if initConfigured && (c.Init.User == "" || c.Init.Password == "") {
		return errors.New("init.user and init.password are required when init is configured")
	}
	if c.Remora.UserLoginWatchdog.Enabled {
		if c.Remora.UserLoginWatchdog.User == "" || c.Remora.UserLoginWatchdog.Password == "" {
			return errors.New("remora.monitoring.user-login.user and password are required when user-login monitoring is enabled")
		}
		if c.Remora.UserLoginWatchdog.Heartbeat < 1 {
			return errors.New("remora.monitoring.user-login.interval must be positive when user-login monitoring is enabled")
		}
	}
	return nil
}

func durationTicks(interval, tick time.Duration) int {
	if interval <= 0 || tick <= 0 {
		return 1
	}
	return max(1, int((interval+tick-1)/tick))
}

func (c *Config) JellyfinURL() string {
	base := strings.Trim(c.Jellyfin.Networking.ServerAddressSettings.BaseURL, "/")
	url := fmt.Sprintf("http://127.0.0.1:%d", c.Jellyfin.Networking.ServerAddressSettings.LocalHTTPPort)
	if base != "" {
		url += "/" + base
	}
	return url
}

func (c JellyfinConfig) HasManagedSettings() bool {
	g := c.General
	b := c.Branding
	t := c.Playback.Transcoding
	n := c.Networking
	s := n.ServerAddressSettings
	return g.Settings.ServerName.Set || g.Paths.CachePath.Set || g.Paths.MetadataPath.Set ||
		g.Performance.ParallelLibraryScanTasksLimit.Set || g.Performance.ParallelImageEncodingLimit.Set ||
		b.EnableSplashScreen.Set || b.SplashScreenImage.Set || b.LoginDisclaimer.Set || b.CustomCSSCode.Set ||
		t.TranscodePath.Set || t.EnableFallbackFonts.Set || t.FallbackFontFolderPath.Set ||
		s.LocalHTTPPortConfigured || s.LocalHTTPSPortConfigured || s.EnableHTTPSConfigured || s.BaseURLConfigured ||
		s.BindToLocalNetworkAddress.Set || n.IPProtocols.EnableIPv4.Set || n.IPProtocols.EnableIPv6.Set
}
