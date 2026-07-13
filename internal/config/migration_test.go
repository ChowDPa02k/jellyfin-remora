package config

import (
	"strings"
	"testing"
)

func TestMigrateLegacyConfigurationToCurrentVersion(t *testing.T) {
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
	if report.FromVersion != 0 || report.ToVersion != 2 || len(report.Applied) != 2 {
		t.Fatalf("report = %#v", report)
	}
	text := string(got)
	for _, want := range []string{"config-version: 2", "monitoring:", "jellyfin-api:", "interval: 7s", "heartbeat: 4"} {
		if !strings.Contains(text, want) {
			t.Fatalf("migrated config does not contain %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "hearbeat") || strings.Contains(text, "health-api-heartbeat") {
		t.Fatalf("migrated config retained legacy key:\n%s", text)
	}
}

func TestMigrateCurrentConfigurationIsSemanticallyUnchanged(t *testing.T) {
	input := []byte("config-version: 2\nrestapi:\n  port: 8095\n")
	got, report, err := Migrate(input)
	if err != nil {
		t.Fatal(err)
	}
	if report.FromVersion != 2 || report.ToVersion != 2 || len(report.Applied) != 0 {
		t.Fatalf("report = %#v", report)
	}
	if !strings.Contains(string(got), "port: 8095") {
		t.Fatalf("migrated config lost value:\n%s", got)
	}
}

func TestMigrateRejectsFutureVersionAndAliasConflict(t *testing.T) {
	if _, _, err := Migrate([]byte("config-version: 3\n")); err == nil {
		t.Fatal("future configuration version succeeded")
	}
	conflict := []byte("remora:\n  health-api-hearbeat: 1\n  health-api-heartbeat: 2\n")
	if _, _, err := Migrate(conflict); err == nil {
		t.Fatal("legacy alias conflict succeeded")
	}
}

func TestMigrateV1MonitoringIntervalsPreservesTiming(t *testing.T) {
	input := []byte(`config-version: 1
remora:
  heartbeat-interval: 2s
  health-api-heartbeat: 7
  api-failure-threshold: 4
  user-login-watchdog:
    enabled: true
    heartbeat: 5
    user: remora
    password: secret
`)
	got, report, err := Migrate(input)
	if err != nil {
		t.Fatal(err)
	}
	if report.FromVersion != 1 || report.ToVersion != 2 || len(report.Applied) != 1 {
		t.Fatalf("report=%#v", report)
	}
	text := string(got)
	for _, want := range []string{"interval: 2s", "interval: 14s", "failure-threshold: 4", "interval: 10s", "user-login:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("migration omitted %q:\n%s", want, text)
		}
	}
}
