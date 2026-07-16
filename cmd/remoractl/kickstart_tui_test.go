package main

import (
	"path/filepath"
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
