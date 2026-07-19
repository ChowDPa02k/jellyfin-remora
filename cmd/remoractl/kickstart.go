package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/kickstart"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"
)

type kickstartAnswerFile struct {
	UseDetected      *bool    `yaml:"use-detected"`
	Archive          string   `yaml:"archive"`
	JellyfinHome     string   `yaml:"jellyfin-home"`
	MediaPaths       []string `yaml:"media-paths"`
	ServerName       string   `yaml:"server-name"`
	DisplayLanguage  string   `yaml:"display-language"`
	MetadataLanguage string   `yaml:"metadata-language"`
	MetadataRegion   string   `yaml:"metadata-region"`
	AdminPassword    string   `yaml:"admin-password"`
}

func runKickstart(args []string) error {
	fs := flag.NewFlagSet("remoractl kickstart", flag.ContinueOnError)
	answersPath := fs.String("answers", "", "non-interactive answer YAML (intended for automated deployment and testing)")
	noStart := fs.Bool("no-start", false, "install the platform service without starting it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: remoractl kickstart [--answers FILE] [--no-start]")
	}
	remoraExecutable, err := locateRemoraExecutable()
	if err != nil {
		return err
	}
	detected, found := kickstart.DiscoverInstalled()
	var answers kickstart.Answers
	if *answersPath != "" {
		answers, err = loadKickstartAnswers(*answersPath, detected, found)
	} else {
		answers, err = runKickstartTUI(detected, found)
	}
	if err != nil {
		return err
	}
	answers.RunAsUser, answers.RunAsGroup = kickstartRunIdentity(answers.Home)
	return deployKickstart(answers, remoraExecutable, !*noStart)
}

func loadKickstartAnswers(path string, detected kickstart.Installation, found bool) (kickstart.Answers, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return kickstart.Answers{}, err
	}
	var input kickstartAnswerFile
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&input); err != nil {
		return kickstart.Answers{}, fmt.Errorf("decode kickstart answers: %w", err)
	}
	var installation kickstart.Installation
	useDetected := found && input.Archive == ""
	if input.UseDetected != nil {
		useDetected = *input.UseDetected
	}
	if useDetected {
		if !found {
			return kickstart.Answers{}, errors.New("use-detected was requested but no compatible Jellyfin installation was found")
		}
		installation = detected
	} else {
		if input.Archive == "" {
			return kickstart.Answers{}, errors.New("archive is required when the detected Jellyfin installation is not used")
		}
		archive, err := kickstart.InspectArchive(input.Archive)
		if err != nil {
			return kickstart.Answers{}, err
		}
		validation, err := validateKickstartPackage(context.Background(), input.Archive, func(phase kickstart.PackageValidationPhase) {
			fmt.Fprintf(os.Stderr, "%s…\n", phase)
		})
		if err != nil {
			return kickstart.Answers{}, fmt.Errorf("validate selected Jellyfin package: %w", err)
		}
		archive.VerifiedSHA256 = validation.LocalSHA256
		archive.VerifiedSize = validation.LocalSize
		installation = kickstart.Installation{Archive: &archive}
	}
	home, err := filepath.Abs(input.JellyfinHome)
	if err != nil {
		return kickstart.Answers{}, err
	}
	if installation.Archive != nil {
		installation.Executable = filepath.Join(home, "server", installation.Archive.ExecutableEntry)
		if installation.Archive.WebDirEntry != "" {
			installation.WebDir = filepath.Join(home, "server", installation.Archive.WebDirEntry)
		}
	}
	return kickstart.Answers{
		Installation: installation, Home: home, MediaPaths: input.MediaPaths,
		ServerName: input.ServerName, DisplayLanguage: input.DisplayLanguage,
		MetadataLanguage: input.MetadataLanguage, MetadataRegion: input.MetadataRegion,
		AdminPassword: input.AdminPassword,
	}, nil
}

func deployKickstart(answers kickstart.Answers, remoraExecutable string, start bool) error {
	if err := prepareKickstartHome(answers); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	disks, err := kickstart.InferStorage(ctx, platform.New(), answers.Home, answers.MediaPaths)
	if err != nil {
		return err
	}
	watchdogPassword, err := kickstart.RandomWatchdogPassword()
	if err != nil {
		return err
	}
	configuration, err := kickstart.BuildConfiguration(answers, disks, watchdogPassword)
	if err != nil {
		return err
	}
	temporary, cleanupTemporary, err := createSensitiveTemp("jellyfin-remora-kickstart-*.yaml")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer cleanupTemporary()
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(configuration); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	cfg, err := config.Load(temporaryPath)
	if err != nil {
		return fmt.Errorf("generated kickstart configuration is invalid: %w", err)
	}
	accepted, err := validateInitStorage(cfg, remoraExecutable)
	if err != nil {
		return err
	}
	if err := preparePlatformInitDirectories(cfg, accepted); err != nil {
		return fmt.Errorf("prepare Jellyfin home: %w", err)
	}
	if answers.Installation.Archive != nil {
		installed, err := kickstart.ExtractArchive(*answers.Installation.Archive, filepath.Join(answers.Home, "server"))
		if err != nil {
			return err
		}
		if installed.Executable != cfg.Jellyfin.Path {
			return fmt.Errorf("extracted Jellyfin executable differs from generated configuration: %s", installed.Executable)
		}
	}
	if err := validateInitPaths(cfg, remoraExecutable); err != nil {
		return err
	}
	workingDir, err := os.Getwd()
	if err != nil {
		return err
	}
	destination := filepath.Join(workingDir, "remora-config.yaml")
	if err := atomicWriteFile(destination, configuration, 0o600); err != nil {
		return fmt.Errorf("write configuration: %w", err)
	}
	serviceExecutable, err := prepareKickstartServiceExecutable(remoraExecutable)
	if err != nil {
		return err
	}
	artifact, err := generatePlatformService(cfg, serviceExecutable, destination)
	if err != nil {
		return err
	}
	fmt.Printf("configuration written: %s\n", destination)
	if artifact == nil {
		return nil
	}
	fmt.Printf("%s generated: %s\n", artifact.Kind, artifact.Path)
	if !initServicePrivileged() {
		fmt.Fprintf(os.Stderr, "WARNING: insufficient privileges to install %s; the generated file was kept in %s\n", artifact.Kind, workingDir)
		fmt.Fprintln(os.Stderr, platformServiceInstallInstructions(artifact))
		return nil
	}
	if err := installInitService(artifact); err != nil {
		return fmt.Errorf("install %s: %w", artifact.Kind, err)
	}
	fmt.Printf("%s installed\n", artifact.Kind)
	if start {
		if err := startInitService(artifact); err != nil {
			return fmt.Errorf("start Jellyfin Remora: %w", err)
		}
		fmt.Println("Jellyfin Remora started")
	}
	return nil
}

func kickstartRunIdentity(home string) (string, string) {
	if runtime.GOOS == "windows" {
		return "", ""
	}
	if runtime.GOOS == "linux" {
		if account, err := user.Lookup("jellyfin"); err == nil {
			group := "jellyfin"
			if resolved, err := user.LookupGroupId(account.Gid); err == nil {
				group = resolved.Name
			}
			return account.Username, group
		}
	}
	username := os.Getenv("SUDO_USER")
	if username == "" || username == "root" {
		if current, err := user.Current(); err == nil {
			username = current.Username
		}
	}
	if username == "root" {
		if info, err := os.Stat(filepath.Dir(home)); err == nil {
			_ = info // ownership fallback is platform-specific; root remains safe when unavailable.
		}
	}
	group := ""
	if account, err := user.Lookup(username); err == nil {
		if resolved, err := user.LookupGroupId(account.Gid); err == nil {
			group = resolved.Name
		}
	}
	return username, group
}

var _ tea.Model = (*kickstartModel)(nil)
