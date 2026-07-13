package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/buildinfo"
	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/control"
	"github.com/ChowDPa02K/jellyfin-remora/internal/jellyfin"
	"github.com/ChowDPa02K/jellyfin-remora/internal/logging"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
	"github.com/ChowDPa02K/jellyfin-remora/internal/probe"
	"github.com/ChowDPa02K/jellyfin-remora/internal/procmanager"
	"github.com/ChowDPa02K/jellyfin-remora/internal/storage"
	"github.com/ChowDPa02K/jellyfin-remora/internal/supervisor"
)

var effectiveUID = os.Geteuid

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "jellyfin-remora:", err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) > 1 && os.Args[1] == "internal-probe" {
		return runProbe(os.Args[2:])
	}
	if len(os.Args) > 1 && os.Args[1] == "validate-config" {
		return runValidateConfig(os.Args[2:])
	}
	fs := flag.NewFlagSet("jellyfin-remora", flag.ContinueOnError)
	configPath := fs.String("c", "config.yml", "configuration file")
	showVersion := fs.Bool("version", false, "show version")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println(buildinfo.Current("jellyfin-remora"))
		return nil
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	instanceLock, err := acquireInstanceLock(cfg.RESTAPI.UnixSocket)
	if err != nil {
		return err
	}
	defer instanceLock.Close()
	logger, closer := newLogger(cfg)
	if closer != nil {
		defer closer.Close()
	}
	slog.SetDefault(logger)
	if st, statErr := os.Stat(*configPath); statErr == nil && st.Mode().Perm()&0077 != 0 {
		logger.Warn("configuration file is readable by group or others; use chmod 0600 when it contains credentials", "mode", st.Mode().Perm())
	}
	backend := platform.New()
	pm, err := procmanager.New(cfg, backend, loggerWriter{logger, "jellyfin_stdout"}, loggerWriter{logger, "jellyfin_stderr"})
	if err != nil {
		return err
	}
	sc, err := storage.New(cfg, backend)
	if err != nil {
		return err
	}
	jc := jellyfin.New(cfg.JellyfinURL(), cfg.Remora.IOTimeout.Duration)
	sup := supervisor.New(cfg, pm, sc, jc, logger)
	api := control.New(cfg, sup, logger)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	errCh := make(chan error, 2)
	go func() { errCh <- sup.Run(ctx) }()
	go func() { errCh <- api.Run(ctx) }()
	err = <-errCh
	cancel()
	<-errCh
	return err
}

type validationReport struct {
	Valid         bool                  `json:"valid"`
	ConfigVersion int                   `json:"config_version"`
	Executable    string                `json:"executable,omitempty"`
	Warnings      []string              `json:"warnings,omitempty"`
	Prepared      []string              `json:"prepared,omitempty"`
	Storage       []model.StorageResult `json:"storage"`
}
type validationFailed struct{}

func (validationFailed) Error() string { return "configuration validation failed" }

func runValidateConfig(args []string) error {
	fs := flag.NewFlagSet("validate-config", flag.ContinueOnError)
	configPath := fs.String("c", "config.yml", "configuration file")
	asJSON := fs.Bool("json", false, "print JSON report")
	prepare := fs.Bool("prepare", false, "create missing Jellyfin directories on verified storage")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	report := validationReport{Valid: true, ConfigVersion: cfg.ConfigVersion}
	if cfg.LegacyConfig {
		report.Warnings = append(report.Warnings, "config-version is missing; version 1 compatibility was assumed")
	}
	if st, statErr := os.Stat(*configPath); statErr == nil && st.Mode().Perm()&0077 != 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("configuration mode %04o exposes credentials to group or others; use 0600", st.Mode().Perm()))
	}
	backend := platform.New()
	pm, err := procmanager.New(cfg, backend, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	report.Executable = pm.Executable()
	checker, err := storage.New(cfg, backend)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Remora.IOTimeout.Duration*time.Duration(max(1, len(cfg.Disks)+4)))
	defer cancel()
	fatal := false
	for i := range cfg.Disks {
		result := checker.InspectDisk(ctx, i)
		report.Storage = append(report.Storage, result)
		if result.Fatal {
			fatal = true
		}
	}
	if !fatal {
		if *prepare {
			prepared, prepareErr := prepareJellyfinPaths(cfg, report.Storage)
			if prepareErr != nil {
				return prepareErr
			}
			report.Prepared = prepared
		}
		for _, result := range checker.CheckPaths(ctx) {
			report.Storage = append(report.Storage, result)
			if result.Fatal {
				fatal = true
			}
		}
	}
	report.Valid = !fatal
	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(report)
	} else {
		fmt.Printf("config-version: %d\nexecutable: %s\n", report.ConfigVersion, report.Executable)
		for _, warning := range report.Warnings {
			fmt.Printf("warning: %s\n", warning)
		}
		for _, path := range report.Prepared {
			fmt.Printf("prepared: %s\n", path)
		}
		for _, result := range report.Storage {
			state := "healthy"
			if result.Fatal {
				state = "fatal"
			} else if !result.Healthy {
				state = "degraded"
			}
			fmt.Printf("storage[%d] %s: %s (%s)\n", result.Index, result.Type, result.Target, state)
			if result.Message != "" {
				fmt.Printf("  %s\n", result.Message)
			}
		}
		fmt.Printf("valid: %t\n", report.Valid)
	}
	if !report.Valid {
		return validationFailed{}
	}
	return nil
}

func prepareJellyfinPaths(cfg *config.Config, disks []model.StorageResult) ([]string, error) {
	if effectiveUID() == 0 {
		return nil, errors.New("--prepare must run as jellyfin.run-as-user, not root")
	}
	paths := []string{cfg.Jellyfin.DataDir, cfg.Jellyfin.ConfigDir, cfg.Jellyfin.CacheDir, cfg.Jellyfin.LogDir}
	var prepared []string
	for _, path := range paths {
		if st, err := os.Stat(path); err == nil {
			if !st.IsDir() {
				return prepared, fmt.Errorf("Jellyfin path is not a directory: %s", path)
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return prepared, err
		}
		covered := false
		for i, disk := range cfg.Disks {
			if i >= len(disks) || !disks[i].Healthy || disks[i].Fatal {
				continue
			}
			rel, err := filepath.Rel(disk.Target, path)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				covered = true
				break
			}
		}
		if !covered {
			return prepared, fmt.Errorf("refusing to create %s: it is not beneath verified configured storage", path)
		}
		if err := os.MkdirAll(path, 0750); err != nil {
			return prepared, err
		}
		prepared = append(prepared, path)
	}
	return prepared, nil
}

func runProbe(args []string) error {
	fs := flag.NewFlagSet("internal-probe", flag.ContinueOnError)
	path := fs.String("path", "", "path")
	permission := fs.String("permission", "r", "r or rw")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return errors.New("--path is required")
	}
	return probe.Path(*path, *permission)
}
func newLogger(cfg *config.Config) (*slog.Logger, io.Closer) {
	level := slog.LevelInfo
	switch cfg.Remora.Logs.Level {
	case "debug":
		level = slog.LevelDebug
	case "warning", "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}
	logPath := safeLogPath(cfg)
	f, err := logging.New(logPath, cfg.Remora.Logs.RotationSizeMB*1024*1024, cfg.Remora.Logs.RotationTime.Duration, cfg.Remora.Logs.PreserveTime.Duration)
	if err != nil {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts)), nil
	}
	return slog.New(slog.NewJSONHandler(io.MultiWriter(os.Stderr, f), opts)), f
}

func safeLogPath(cfg *config.Config) string {
	configured := filepath.Join(cfg.Remora.Logs.Path, "jellyfin-remora.log")
	for _, d := range cfg.Disks {
		rel, err := filepath.Rel(d.Target, configured)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if effectiveUID() == 0 {
				return "/var/log/jellyfin-remora/jellyfin-remora.log"
			}
			home, _ := os.UserHomeDir()
			return filepath.Join(home, "Library", "Logs", "Jellyfin Remora", "jellyfin-remora.log")
		}
	}
	return configured
}

type loggerWriter struct {
	log    *slog.Logger
	stream string
}

func (w loggerWriter) Write(p []byte) (int, error) {
	w.log.Info(w.stream, "message", string(p))
	return len(p), nil
}
