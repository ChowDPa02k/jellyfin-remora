package config

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

const CurrentVersion = 1

type MigrationReport struct {
	FromVersion int
	ToVersion   int
	Applied     []string
}

type migration struct {
	from  int
	to    int
	name  string
	apply func(*yaml.Node) error
}

var migrations = []migration{
	{from: 0, to: 1, name: "legacy-unversioned-to-v1", apply: migrateLegacyToV1},
}

// Migrate converts configuration bytes in memory. It never writes the source file.
func Migrate(data []byte) ([]byte, MigrationReport, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, MigrationReport{}, fmt.Errorf("parse config for migration: %w", err)
	}
	root, err := mappingRoot(&document)
	if err != nil {
		return nil, MigrationReport{}, err
	}
	version, err := configVersion(root)
	if err != nil {
		return nil, MigrationReport{}, err
	}
	report := MigrationReport{FromVersion: version, ToVersion: version}
	if version > CurrentVersion {
		return nil, report, fmt.Errorf("unsupported config-version %d; current version is %d", version, CurrentVersion)
	}
	for version < CurrentVersion {
		m, ok := migrationFrom(version)
		if !ok {
			return nil, report, fmt.Errorf("no configuration migration from version %d", version)
		}
		if m.to <= m.from {
			return nil, report, fmt.Errorf("invalid configuration migration %q", m.name)
		}
		if err := m.apply(root); err != nil {
			return nil, report, fmt.Errorf("configuration migration %q: %w", m.name, err)
		}
		version = m.to
		report.ToVersion = version
		report.Applied = append(report.Applied, m.name)
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return nil, report, fmt.Errorf("encode migrated config: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, report, fmt.Errorf("close migrated config encoder: %w", err)
	}
	return out.Bytes(), report, nil
}

func migrationFrom(version int) (migration, bool) {
	for _, candidate := range migrations {
		if candidate.from == version {
			return candidate, true
		}
	}
	return migration{}, false
}

func mappingRoot(document *yaml.Node) (*yaml.Node, error) {
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("configuration root must be a mapping")
	}
	return document.Content[0], nil
}

func configVersion(root *yaml.Node) (int, error) {
	value := mappingValue(root, "config-version")
	if value == nil {
		return 0, nil
	}
	var version int
	if err := value.Decode(&version); err != nil || version < 1 {
		return 0, fmt.Errorf("config-version must be a positive integer")
	}
	return version, nil
}

func migrateLegacyToV1(root *yaml.Node) error {
	if mappingValue(root, "config-version") != nil {
		return fmt.Errorf("legacy migration received an already-versioned configuration")
	}
	root.Content = append([]*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "config-version"},
		{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"},
	}, root.Content...)
	if remora := mappingValue(root, "remora"); remora != nil {
		if err := renameKey(remora, "health-api-hearbeat", "health-api-heartbeat"); err != nil {
			return err
		}
	}
	if disks := mappingValue(root, "disk"); disks != nil && disks.Kind == yaml.SequenceNode {
		for index, disk := range disks.Content {
			if err := renameKey(disk, "hearbeat", "heartbeat"); err != nil {
				return fmt.Errorf("disk[%d]: %w", index, err)
			}
		}
	}
	return nil
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func renameKey(mapping *yaml.Node, oldKey, newKey string) error {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	oldIndex, newIndex := -1, -1
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		switch mapping.Content[i].Value {
		case oldKey:
			oldIndex = i
		case newKey:
			newIndex = i
		}
	}
	if oldIndex >= 0 && newIndex >= 0 {
		return fmt.Errorf("both %q and %q are set", oldKey, newKey)
	}
	if oldIndex >= 0 {
		mapping.Content[oldIndex].Value = newKey
	}
	return nil
}
