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
	"path/filepath"
	"strings"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/buildinfo"
	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/control"
	"github.com/ChowDPa02K/jellyfin-remora/internal/databasemonitor"
	"github.com/ChowDPa02K/jellyfin-remora/internal/jellyfin"
	"github.com/ChowDPa02K/jellyfin-remora/internal/logging"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
	"github.com/ChowDPa02K/jellyfin-remora/internal/probe"
	"github.com/ChowDPa02K/jellyfin-remora/internal/procmanager"
	"github.com/ChowDPa02K/jellyfin-remora/internal/storage"
	"github.com/ChowDPa02K/jellyfin-remora/internal/supervisor"
	"github.com/ChowDPa02K/jellyfin-remora/sample"
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
	serviceMode := fs.Bool("service", false, "run under the Windows Service Control Manager")
	showVersion := fs.Bool("version", false, "show version")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println(buildinfo.Current("jellyfin-remora"))
		return nil
	}
	activeConfigPath, err := resolveActiveConfigPath(*configPath)
	if err != nil {
		return fmt.Errorf("resolve configuration path: %w", err)
	}
	runner := func(ctx context.Context) error { return runDaemon(ctx, activeConfigPath) }
	if *serviceMode {
		return runPlatformService(runner)
	}
	ctx, cancel := platformSignalContext()
	defer cancel()
	return runner(ctx)
}

func runDaemon(ctx context.Context, activeConfigPath string) error {
	cfg, err := config.Load(activeConfigPath)
	if err != nil {
		return err
	}
	instanceLock, err := acquireInstanceLock(cfg)
	if err != nil {
		return err
	}
	defer instanceLock.Close()
	writeStartupSplash(os.Stdout)
	logger, closer := newLogger(cfg)
	if closer != nil {
		defer closer.Close()
	}
	slog.SetDefault(logger)
	for _, warning := range configFileWarnings(activeConfigPath) {
		logger.Warn("configuration security warning", "warning", warning)
	}
	backend := platform.New()
	if preparer, ok := backend.(interface{ PrepareSupervisor() error }); ok {
		if err := preparer.PrepareSupervisor(); err != nil {
			return fmt.Errorf("prepare platform supervisor: %w", err)
		}
	}
	jellyfinLogPath := safeJellyfinLogPath(cfg)
	jellyfinLog, err := logging.New(jellyfinLogPath, cfg.Remora.Logs.RotationSizeMB*1024*1024, cfg.Remora.Logs.RotationTime.Duration, cfg.Remora.Logs.PreserveTime.Duration)
	if err != nil {
		return fmt.Errorf("open Jellyfin console log: %w", err)
	}
	defer jellyfinLog.Close()
	databaseDetector := &databasemonitor.Detector{}
	consoleWriter := io.MultiWriter(jellyfinLog, databaseDetector)
	pm, err := procmanager.New(cfg, backend, consoleWriter, consoleWriter)
	if err != nil {
		return err
	}
	warnExecutableProvenance(logger, backend, pm.Executable())
	sc, err := storage.New(cfg, backend)
	if err != nil {
		return err
	}
	jc := jellyfin.New(cfg.JellyfinURL(), cfg.Remora.IOTimeout.Duration)
	sup := supervisor.New(cfg, pm, sc, jc, logger)
	sup.SetDatabaseDamageSource(databaseDetector)
	api := control.NewWithOptions(cfg, sup, logger, control.Options{ConfigPath: activeConfigPath, LogPath: safeLogPath(cfg), JellyfinLogPath: jellyfinLogPath})
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 2)
	go func() { errCh <- sup.Run(ctx) }()
	go func() { errCh <- api.Run(ctx) }()
	err = <-errCh
	cancel()
	<-errCh
	return err
}

func writeStartupSplash(w io.Writer) {
	if len(sample.SplashASCII) == 0 {
		return
	}
	_, _ = w.Write(sample.SplashASCII)
	if sample.SplashASCII[len(sample.SplashASCII)-1] != '\n' {
		_, _ = io.WriteString(w, "\n")
	}
}

func resolveActiveConfigPath(path string) (string, error) {
	return filepath.Abs(path)
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
		report.Warnings = append(report.Warnings, "config-version is missing; legacy configuration was migrated to version 2 in memory")
	}
	report.Warnings = append(report.Warnings, configFileWarnings(*configPath)...)
	backend := platform.New()
	pm, err := procmanager.New(cfg, backend, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	report.Executable = pm.Executable()
	if found, inspectErr := backend.ExecutableProvenance(pm.Executable()); inspectErr != nil {
		report.Warnings = append(report.Warnings, "cannot inspect Jellyfin executable provenance: "+inspectErr.Error())
	} else if found {
		report.Warnings = append(report.Warnings, provenanceWarning)
	}
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

const provenanceWarning = "Jellyfin executable has com.apple.provenance metadata; Gatekeeper may block an unsigned tarball executable"

type provenanceInspector interface {
	ExecutableProvenance(string) (bool, error)
}

func warnExecutableProvenance(logger *slog.Logger, inspector provenanceInspector, executable string) {
	found, err := inspector.ExecutableProvenance(executable)
	if err != nil {
		logger.Warn("cannot inspect Jellyfin executable provenance", "executable", executable, "error", err)
		return
	}
	if found {
		logger.Warn(provenanceWarning, "executable", executable, "xattr", "com.apple.provenance")
	}
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
		handler, platformCloser := withPlatformLogging(slog.NewJSONHandler(os.Stderr, opts))
		return slog.New(handler), platformCloser
	}
	handler, platformCloser := withPlatformLogging(slog.NewJSONHandler(io.MultiWriter(os.Stderr, f), opts))
	return slog.New(handler), combineClosers(f, platformCloser)
}

type closers []io.Closer

func (c closers) Close() error {
	var first error
	for _, closer := range c {
		if closer != nil {
			if err := closer.Close(); err != nil && first == nil {
				first = err
			}
		}
	}
	return first
}

func combineClosers(values ...io.Closer) io.Closer {
	var combined closers
	for _, value := range values {
		if value != nil {
			combined = append(combined, value)
		}
	}
	if len(combined) == 0 {
		return nil
	}
	return combined
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

func safeJellyfinLogPath(cfg *config.Config) string {
	return filepath.Join(filepath.Dir(safeLogPath(cfg)), "jellyfin-console.log")
}
