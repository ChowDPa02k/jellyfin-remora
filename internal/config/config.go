package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration struct{ time.Duration }

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
	ConfigVersion int            `yaml:"config-version"`
	LegacyConfig  bool           `yaml:"-"`
	RESTAPI       RESTAPIConfig  `yaml:"restapi"`
	Remora        RemoraConfig   `yaml:"remora"`
	Disks         []DiskConfig   `yaml:"disk"`
	Jellyfin      JellyfinConfig `yaml:"jellyfin"`
	Init          InitConfig     `yaml:"init,omitempty"`
}

type RESTAPIConfig struct {
	Listen     string `yaml:"listen"`
	Port       int    `yaml:"port"`
	UnixSocket string `yaml:"unix-socket"`
}

type RemoraConfig struct {
	ServerStartTimeout  Duration                `yaml:"server-start-timeout"`
	ServerStopTimeout   Duration                `yaml:"server-stop-timeout"`
	HeartbeatInterval   Duration                `yaml:"heartbeat-interval"`
	HealthAPIHeartbeat  int                     `yaml:"health-api-heartbeat"`
	HealthAPIHearbeat   int                     `yaml:"health-api-hearbeat,omitempty"`
	IOTimeout           Duration                `yaml:"io-timeout"`
	RecoverySuccesses   int                     `yaml:"recovery-successes"`
	APIFailureThreshold int                     `yaml:"api-failure-threshold"`
	UserLoginWatchdog   UserLoginWatchdogConfig `yaml:"user-login-watchdog,omitempty"`
	DataDir             string                  `yaml:"data-dir"`
	Logs                LogConfig               `yaml:"logs"`
}

type UserLoginWatchdogConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Heartbeat int    `yaml:"heartbeat"`
	User      string `yaml:"user"`
	Password  string `yaml:"password"`
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
	Type       string `yaml:"type"`
	Device     string `yaml:"device"`
	UUID       string `yaml:"uuid"`
	Options    string `yaml:"options"`
	User       string `yaml:"user"`
	Password   string `yaml:"password"`
	Target     string `yaml:"target"`
	Permission string `yaml:"permission"`
	Heartbeat  int    `yaml:"heartbeat"`
	Hearbeat   int    `yaml:"hearbeat,omitempty"`
}

type JellyfinConfig struct {
	Path       string           `yaml:"path"`
	RunAsUser  string           `yaml:"run-as-user"`
	RunAsGroup string           `yaml:"run-as-group"`
	DataDir    string           `yaml:"data-dir"`
	ConfigDir  string           `yaml:"config-dir"`
	CacheDir   string           `yaml:"cache-dir"`
	LogDir     string           `yaml:"log-dir"`
	WebDir     string           `yaml:"web-dir"`
	Parameters map[string]any   `yaml:"parameters"`
	General    map[string]any   `yaml:"general,omitempty"`
	Branding   map[string]any   `yaml:"branding,omitempty"`
	Playback   map[string]any   `yaml:"playback,omitempty"`
	Networking NetworkingConfig `yaml:"networking,omitempty"`
}

type NetworkingConfig struct {
	ServerAddressSettings ServerAddressSettings `yaml:"server-address-settings"`
}

type ServerAddressSettings struct {
	LocalHTTPPort  int    `yaml:"local-http-port-number"`
	LocalHTTPSPort int    `yaml:"local-https-port-number"`
	EnableHTTPS    bool   `yaml:"enable-https"`
	BaseURL        string `yaml:"base-url"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	decoder := yaml.NewDecoder(bytes.NewReader(b))
	decoder.KnownFields(true)
	if err := decoder.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.defaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) defaults() {
	if c.ConfigVersion == 0 {
		c.ConfigVersion = 1
		c.LegacyConfig = true
	}
	if c.RESTAPI.Listen == "" {
		c.RESTAPI.Listen = "127.0.0.1"
	}
	if c.RESTAPI.Port == 0 {
		c.RESTAPI.Port = 8095
	}
	if c.RESTAPI.UnixSocket == "" {
		c.RESTAPI.UnixSocket = filepath.Join(os.TempDir(), "jellyfin-remora.sock")
	}
	if c.Remora.ServerStartTimeout.Duration == 0 {
		c.Remora.ServerStartTimeout.Duration = 30 * time.Second
	}
	if c.Remora.ServerStopTimeout.Duration == 0 {
		c.Remora.ServerStopTimeout.Duration = 300 * time.Second
	}
	if c.Remora.HeartbeatInterval.Duration == 0 {
		c.Remora.HeartbeatInterval.Duration = time.Second
	}
	if c.Remora.HealthAPIHeartbeat == 0 {
		c.Remora.HealthAPIHeartbeat = c.Remora.HealthAPIHearbeat
	}
	if c.Remora.HealthAPIHeartbeat == 0 {
		c.Remora.HealthAPIHeartbeat = 10
	}
	if c.Remora.IOTimeout.Duration == 0 {
		c.Remora.IOTimeout.Duration = 5 * time.Second
	}
	if c.Remora.RecoverySuccesses == 0 {
		c.Remora.RecoverySuccesses = 3
	}
	if c.Remora.APIFailureThreshold == 0 {
		c.Remora.APIFailureThreshold = 3
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
	if c.ConfigVersion != 1 {
		return fmt.Errorf("unsupported config-version %d", c.ConfigVersion)
	}
	if ip := net.ParseIP(c.RESTAPI.Listen); ip == nil || !ip.IsLoopback() {
		return errors.New("restapi.listen must be a loopback IP address")
	}
	if c.RESTAPI.Port < 1 || c.RESTAPI.Port > 65535 {
		return errors.New("restapi.port must be between 1 and 65535")
	}
	if c.Remora.HeartbeatInterval.Duration <= 0 || c.Remora.ServerStartTimeout.Duration <= 0 || c.Remora.ServerStopTimeout.Duration <= 0 || c.Remora.IOTimeout.Duration <= 0 {
		return errors.New("remora intervals and timeouts must be positive")
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
	for i, d := range c.Disks {
		c.Disks[i].Type = strings.ToLower(d.Type)
		d = c.Disks[i]
		if d.Type != "physical" && d.Type != "smb" && d.Type != "nfs" {
			return fmt.Errorf("disk[%d].type must be physical, smb, or nfs", i)
		}
		if d.Target == "" || !filepath.IsAbs(d.Target) {
			return fmt.Errorf("disk[%d].target must be absolute", i)
		}
		if d.Permission != "r" && d.Permission != "rw" {
			return fmt.Errorf("disk[%d].permission must be r or rw", i)
		}
		if d.Type == "physical" && (d.Device == "") == (d.UUID == "") {
			return fmt.Errorf("disk[%d] physical disk requires exactly one of device or uuid", i)
		}
		if d.Type != "physical" && d.Device == "" {
			return fmt.Errorf("disk[%d].device is required", i)
		}
	}
	if runtime.GOOS == "darwin" && c.Jellyfin.RunAsUser == "root" {
		return errors.New("refusing to run Jellyfin as root")
	}
	initConfigured := c.Init.User != "" || c.Init.Password != "" || c.Init.ServerName != "" || c.Init.DisplayLanguage != "" || c.Init.PreferredMetadataLanguage != "" || c.Init.PreferredMetadataRegion != ""
	if initConfigured && (c.Init.User == "" || c.Init.Password == "") {
		return errors.New("init.user and init.password are required when init is configured")
	}
	if c.Remora.UserLoginWatchdog.Enabled {
		if c.Remora.UserLoginWatchdog.User == "" || c.Remora.UserLoginWatchdog.Password == "" {
			return errors.New("remora.user-login-watchdog.user and password are required when the watchdog is enabled")
		}
		if c.Remora.UserLoginWatchdog.Heartbeat < 1 {
			return errors.New("remora.user-login-watchdog.heartbeat must be positive when the watchdog is enabled")
		}
	}
	return nil
}

func (c *Config) JellyfinURL() string {
	base := strings.Trim(c.Jellyfin.Networking.ServerAddressSettings.BaseURL, "/")
	url := fmt.Sprintf("http://127.0.0.1:%d", c.Jellyfin.Networking.ServerAddressSettings.LocalHTTPPort)
	if base != "" {
		url += "/" + base
	}
	return url
}
