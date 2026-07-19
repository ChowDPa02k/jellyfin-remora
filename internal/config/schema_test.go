package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSchemaDeclaresTCPEnabledAsBoolean(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "schema", "config.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	properties := schema["properties"].(map[string]any)
	restapi := properties["restapi"].(map[string]any)
	restProperties := restapi["properties"].(map[string]any)
	tcpEnabled, ok := restProperties["tcp-enabled"].(map[string]any)
	if !ok || tcpEnabled["type"] != "boolean" {
		t.Fatalf("schema restapi.tcp-enabled = %#v, want boolean", restProperties["tcp-enabled"])
	}
}
