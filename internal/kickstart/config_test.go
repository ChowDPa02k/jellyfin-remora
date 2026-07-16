package kickstart

import (
	"context"
	"strings"
	"testing"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
	"gopkg.in/yaml.v3"
)

type staticMounts []platform.MountInfo

func (m staticMounts) Mounts(context.Context) ([]platform.MountInfo, error) { return m, nil }

func TestInferStorageUsesLongestMountAndDeduplicates(t *testing.T) {
	homeParent := t.TempDir()
	home := homeParent + "/new/home"
	disks, err := InferStorage(context.Background(), staticMounts{
		{Source: "/dev/root", Target: "/", FSType: "ext4"},
		{Source: "/dev/data", Target: homeParent, FSType: "xfs"},
		{Source: "nas:/media", Target: "/media", FSType: "nfs4"},
	}, home, []string{homeParent + "/media", "/media/movies"})
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 2 {
		t.Fatalf("got %d disks: %#v", len(disks), disks)
	}
	if disks[0].Target != "/media" || disks[0].Type != "nfs" || disks[0].Permission != "r" {
		t.Fatalf("unexpected NFS disk: %#v", disks[0])
	}
	if disks[1].Target != homeParent || disks[1].Type != "physical" || disks[1].Permission != "rw" || disks[1].ProbePath != homeParent {
		t.Fatalf("unexpected physical disk: %#v", disks[1])
	}
}

func TestBuildConfigurationOmitsAdvancedSections(t *testing.T) {
	data, err := BuildConfiguration(Answers{
		Installation: Installation{Executable: "/opt/jellyfin/jellyfin", WebDir: "/opt/jellyfin/jellyfin-web"},
		Home:         "/srv/jellyfin", ServerName: "Kickstart Test", DisplayLanguage: "Deutsch",
		MetadataLanguage: "German", MetadataRegion: "Germany", AdminPassword: "admin-secret",
		RunAsUser: "jellyfin", RunAsGroup: "jellyfin",
	}, []config.DiskConfig{{
		Type: "physical", Device: "/dev/disk1s1", Target: "/srv",
		ProbePath: "/srv", Permission: "rw", Heartbeat: 1, FailureThreshold: 1,
	}}, "Ab3dE6gH9jK2mN5p")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateGeneratedConfiguration(data); err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	jellyfin := root["jellyfin"].(map[string]any)
	for _, forbidden := range []string{"parameters", "general", "networking"} {
		if _, exists := jellyfin[forbidden]; exists {
			t.Errorf("forbidden jellyfin.%s was emitted\n%s", forbidden, data)
		}
	}
	branding := jellyfin["branding"].(map[string]any)
	if branding["login-disclaimer"] != "Powered by Jellyfin Remora" {
		t.Fatalf("unexpected branding: %#v", branding)
	}
	if !strings.Contains(string(data), "transcode-path: /srv/jellyfin/transcode") {
		t.Fatalf("transcode path missing\n%s", data)
	}
}

func TestLocalizationCatalogIncludesNonEnglishSelections(t *testing.T) {
	catalog, err := Localizations()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"العربية", "한국어", "Deutsch"} {
		if !contains(catalog.DisplayLanguages, value) {
			t.Errorf("display language %q is missing", value)
		}
	}
	for _, value := range []string{"Arabic", "Korean", "German"} {
		if !contains(catalog.MetadataLanguages, value) {
			t.Errorf("metadata language %q is missing", value)
		}
	}
}
