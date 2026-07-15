package sample

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEveryYAMLTemplateIsEmbedded(t *testing.T) {
	paths, err := filepath.Glob("*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("sample directory contains no YAML templates")
	}
	for _, path := range paths {
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Files.ReadFile(filepath.Base(path))
		if err != nil {
			t.Fatalf("template %s is not embedded: %v", path, err)
		}
		if string(got) != string(want) {
			t.Fatalf("embedded template %s differs from its source", path)
		}
	}
}
