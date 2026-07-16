package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChowDPa02K/jellyfin-remora/internal/kickstart"
)

func TestLoadKickstartAnswersRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "answers.yaml")
	if err := os.WriteFile(path, []byte("use-detected: true\nserver-naem: typo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := loadKickstartAnswers(path, kickstart.Installation{Executable: "/jellyfin"}, true)
	if err == nil || !strings.Contains(err.Error(), "server-naem") {
		t.Fatalf("error = %v", err)
	}
}
