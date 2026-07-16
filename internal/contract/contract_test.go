package contract_test

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ChowDPa02K/jellyfin-remora/internal/contract"
	publicschema "github.com/ChowDPa02K/jellyfin-remora/schema"
)

func TestCompatibilityManifestMatchesCodeContract(t *testing.T) {
	var manifest struct {
		Baseline string `json:"baseline"`
		Config   struct {
			Current int `json:"current_version"`
		} `json:"config"`
		API struct {
			Major           int                  `json:"major"`
			VersionHeader   string               `json:"version_header"`
			OperationHeader string               `json:"operation_header"`
			Operations      []contract.Operation `json:"operations"`
			Errors          map[string]int       `json:"errors"`
		} `json:"api"`
		CLI struct {
			ExitCodes map[string]int `json:"exit_codes"`
		} `json:"cli"`
		Platforms struct {
			Darwin  map[string]any `json:"darwin"`
			Linux   map[string]any `json:"linux"`
			Windows map[string]any `json:"windows"`
		} `json:"platforms"`
	}
	if err := json.Unmarshal(publicschema.CompatibilityManifest, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Baseline != contract.Baseline || manifest.Config.Current != contract.ConfigVersion || manifest.API.Major != contract.APIVersion {
		t.Fatalf("manifest versions drifted: %+v", manifest)
	}
	if manifest.API.VersionHeader != contract.APIHeaderVersion || manifest.API.OperationHeader != contract.APIHeaderOperationID {
		t.Fatalf("manifest headers drifted: %+v", manifest.API)
	}
	if !reflect.DeepEqual(manifest.API.Operations, contract.APIOperations) || !reflect.DeepEqual(manifest.API.Errors, contract.APIErrorStatus) {
		t.Fatal("manifest API operations or errors drifted from the code contract")
	}
	wantExits := map[string]int{"success": 0, "internal": 1, "usage": 2, "unavailable": 3, "conflict": 4, "timeout": 5}
	if !reflect.DeepEqual(manifest.CLI.ExitCodes, wantExits) {
		t.Fatalf("manifest exit codes=%v, want %v", manifest.CLI.ExitCodes, wantExits)
	}
	assertString(t, manifest.Platforms.Darwin, "service", contract.DarwinServiceLabel)
	assertString(t, manifest.Platforms.Linux, "service", contract.LinuxServiceName)
	assertString(t, manifest.Platforms.Linux, "config", contract.LinuxConfigPath)
	assertString(t, manifest.Platforms.Linux, "socket", contract.LinuxSocketPath)
	assertString(t, manifest.Platforms.Windows, "service", contract.WindowsServiceName)
	assertString(t, manifest.Platforms.Windows, "task", contract.WindowsTaskName)
	assertString(t, manifest.Platforms.Windows, "named_pipe", contract.WindowsNamedPipe)
}

func TestPackagedServiceDefinitionsHonorFrozenIdentity(t *testing.T) {
	for _, fixture := range []struct {
		path string
		want []string
	}{
		{"../../packaging/linux/jellyfin-remora.service", []string{contract.LinuxConfigPath, contract.LinuxDaemonPath, contract.LinuxServiceName[:len(contract.LinuxServiceName)-len(".service")]}},
		{"../../packaging/io.github.chowdpa02k.jellyfin-remora.plist", []string{contract.DarwinServiceLabel, contract.DarwinStdoutPath, contract.DarwinStderrPath}},
	} {
		data, err := os.ReadFile(fixture.path)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range fixture.want {
			if !strings.Contains(string(data), want) {
				t.Fatalf("%s does not contain frozen value %q", fixture.path, want)
			}
		}
	}
}

func assertString(t *testing.T, values map[string]any, key, want string) {
	t.Helper()
	if got, _ := values[key].(string); got != want {
		t.Fatalf("%s=%q, want %q", key, got, want)
	}
}
