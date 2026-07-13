package config

import (
	"strings"
	"testing"
)

func TestMigrateLegacyConfigurationToV1(t *testing.T) {
	input := []byte(`remora:
  health-api-hearbeat: 7
disk:
  - type: smb
    hearbeat: 4
`)
	got, report, err := Migrate(input)
	if err != nil {
		t.Fatal(err)
	}
	if report.FromVersion != 0 || report.ToVersion != 1 || len(report.Applied) != 1 {
		t.Fatalf("report = %#v", report)
	}
	text := string(got)
	for _, want := range []string{"config-version: 1", "health-api-heartbeat: 7", "heartbeat: 4"} {
		if !strings.Contains(text, want) {
			t.Fatalf("migrated config does not contain %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "hearbeat") {
		t.Fatalf("migrated config retained legacy key:\n%s", text)
	}
}

func TestMigrateCurrentConfigurationIsSemanticallyUnchanged(t *testing.T) {
	input := []byte("config-version: 1\nrestapi:\n  port: 8095\n")
	got, report, err := Migrate(input)
	if err != nil {
		t.Fatal(err)
	}
	if report.FromVersion != 1 || report.ToVersion != 1 || len(report.Applied) != 0 {
		t.Fatalf("report = %#v", report)
	}
	if !strings.Contains(string(got), "port: 8095") {
		t.Fatalf("migrated config lost value:\n%s", got)
	}
}

func TestMigrateRejectsFutureVersionAndAliasConflict(t *testing.T) {
	if _, _, err := Migrate([]byte("config-version: 2\n")); err == nil {
		t.Fatal("future configuration version succeeded")
	}
	conflict := []byte("remora:\n  health-api-hearbeat: 1\n  health-api-heartbeat: 2\n")
	if _, _, err := Migrate(conflict); err == nil {
		t.Fatal("legacy alias conflict succeeded")
	}
}
