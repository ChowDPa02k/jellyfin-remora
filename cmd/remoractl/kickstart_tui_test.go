package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ChowDPa02K/jellyfin-remora/internal/kickstart"
	tea "github.com/charmbracelet/bubbletea"
)

func TestKickstartTUIInstalledPackageFlow(t *testing.T) {
	catalog := kickstart.LocalizationCatalog{
		DisplayLanguages: []string{"Deutsch"}, MetadataLanguages: []string{"German"}, MetadataRegions: []string{"Germany"},
	}
	model := newKickstartModel(kickstart.Installation{Executable: "/opt/jellyfin/jellyfin", WebDir: "/opt/jellyfin/web"}, true, catalog)
	model = updateKickstart(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.stage != stageHome {
		t.Fatalf("stage = %v", model.stage)
	}
	home := filepath.Join(t.TempDir(), "jellyfin home")
	model = typeKickstart(t, model, home)
	model = updateKickstart(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.stage != stageMedia {
		t.Fatalf("stage = %v", model.stage)
	}
	model = typeKickstart(t, model, "/media/movies")
	model = updateKickstart(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = updateKickstart(t, model, tea.KeyMsg{Type: tea.KeyCtrlD})
	model = typeKickstart(t, model, "Kickstart TUI")
	model = updateKickstart(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	for _, stage := range []kickstartStage{stageDisplayLanguage, stageMetadataLanguage, stageMetadataRegion} {
		if model.stage != stage {
			t.Fatalf("stage = %v, want %v", model.stage, stage)
		}
		model = updateKickstart(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	}
	model = typeKickstart(t, model, "secret-password")
	model = updateKickstart(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.stage != stageSubmit {
		t.Fatalf("stage = %v", model.stage)
	}
	model = updateKickstart(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if !model.done {
		t.Fatal("model did not complete")
	}
	if model.answers.Home != home || model.answers.ServerName != "Kickstart TUI" || len(model.answers.MediaPaths) != 1 {
		t.Fatalf("answers = %#v", model.answers)
	}
	if model.answers.DisplayLanguage != "Deutsch" || model.answers.MetadataLanguage != "German" || model.answers.MetadataRegion != "Germany" {
		t.Fatalf("localizations = %#v", model.answers)
	}
}

func TestKickstartTUIMediaRequiresPathOrDone(t *testing.T) {
	model := kickstartModel{stage: stageMedia}
	model.setInput("Media path", false)
	model = updateKickstart(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.err == nil || model.stage != stageMedia {
		t.Fatalf("empty media submission did not remain in stage: %#v", model)
	}
}

func TestKickstartTUIPackageValidationShowsNetworkStages(t *testing.T) {
	archivePath := writeKickstartTUIArchive(t)
	previous := validateKickstartPackage
	validateKickstartPackage = func(_ context.Context, path string, progress func(kickstart.PackageValidationPhase)) (kickstart.PackageValidation, error) {
		progress(kickstart.PackageValidationConnecting)
		progress(kickstart.PackageValidationDownloading)
		return kickstart.PackageValidation{Filename: filepath.Base(path), LocalSHA256: strings.Repeat("a", 64), LocalSize: 123, OfficialSHA256: strings.Repeat("a", 64)}, nil
	}
	t.Cleanup(func() { validateKickstartPackage = previous })

	model := newKickstartModel(kickstart.Installation{}, false, kickstart.LocalizationCatalog{})
	command, err := model.beginPackageValidation(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if view := model.View(); !strings.Contains(view, "Connecting Jellyfin repo") {
		t.Fatalf("connecting stage is not visible:\n%s", view)
	}
	commandDone := make(chan struct{})
	go func() {
		_ = command()
		close(commandDone)
	}()
	first := waitForKickstartPackageProgress(model.validationProgress)()
	model = updateKickstart(t, model, first)
	second := waitForKickstartPackageProgress(model.validationProgress)()
	model = updateKickstart(t, model, second)
	if view := model.View(); !strings.Contains(view, "Downloading package") {
		t.Fatalf("download stage is not visible:\n%s", view)
	}
	done := waitForKickstartPackageProgress(model.validationProgress)()
	model = updateKickstart(t, model, done)
	<-commandDone
	if model.stage != stageHome || model.answers.Installation.Archive == nil {
		t.Fatalf("successful validation did not advance: %#v", model)
	}
	if model.answers.Installation.Archive.VerifiedSHA256 == "" || model.answers.Installation.Archive.VerifiedSize != 123 {
		t.Fatalf("repository verification was not bound to archive: %#v", model.answers.Installation.Archive)
	}
}

func writeKickstartTUIArchive(t *testing.T) string {
	t.Helper()
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), fmt.Sprintf("jellyfin_10.11.11-%s.tar.gz", runtime.GOARCH))
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(file)
	archive := tar.NewWriter(compressed)
	if err := archive.WriteHeader(&tar.Header{Name: "jellyfin/jellyfin", Mode: 0o755, Size: int64(len(binary)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func typeKickstart(t *testing.T, model kickstartModel, value string) kickstartModel {
	t.Helper()
	return updateKickstart(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)})
}

func updateKickstart(t *testing.T, model kickstartModel, message tea.Msg) kickstartModel {
	t.Helper()
	updated, _ := model.Update(message)
	result, ok := updated.(kickstartModel)
	if !ok {
		t.Fatalf("unexpected model type %T", updated)
	}
	return result
}
