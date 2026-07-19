package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ChowDPa02K/jellyfin-remora/sample"
	"gopkg.in/yaml.v3"
)

func TestJellyfinEnvironmentContractMatchesStructSchemaAndSamples(t *testing.T) {
	field, ok := reflect.TypeOf(JellyfinConfig{}).FieldByName("Env")
	if !ok || field.Type.Kind() != reflect.Map || field.Type.Key().Kind() != reflect.String || field.Type.Elem().Kind() != reflect.String || field.Tag.Get("yaml") != "env,omitempty" {
		t.Fatalf("JellyfinConfig.Env contract = %#v", field)
	}

	schemaBytes, err := os.ReadFile(filepath.Join("..", "..", "schema", "config.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(schemaBytes, &document); err != nil {
		t.Fatal(err)
	}
	properties := document["properties"].(map[string]any)
	jellyfin := properties["jellyfin"].(map[string]any)["properties"].(map[string]any)
	environment := jellyfin["env"].(map[string]any)
	if environment["type"] != "object" || environment["additionalProperties"].(map[string]any)["type"] != "string" {
		t.Fatalf("schema jellyfin.env contract = %#v", environment)
	}

	entries, err := sample.Files.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			data, err := sample.Files.ReadFile(entry.Name())
			if err != nil {
				t.Fatal(err)
			}
			var root yaml.Node
			if err := yaml.Unmarshal(data, &root); err != nil {
				t.Fatal(err)
			}
			if len(root.Content) == 0 {
				return
			}
			jellyfinNode := mappingValue(root.Content[0], "jellyfin")
			if jellyfinNode == nil {
				t.Fatal("sample omits jellyfin mapping")
			}
			environmentNode := mappingValue(jellyfinNode, "env")
			if environmentNode == nil {
				return
			}
			if environmentNode.Kind != yaml.MappingNode {
				t.Fatalf("jellyfin.env kind = %v", environmentNode.Kind)
			}
			for index := 0; index+1 < len(environmentNode.Content); index += 2 {
				if environmentNode.Content[index].Tag != "!!str" || environmentNode.Content[index+1].Tag != "!!str" {
					t.Fatalf("jellyfin.env entry %q must use string scalars", environmentNode.Content[index].Value)
				}
			}
		})
	}
}
