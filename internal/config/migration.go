package config

import (
	"bytes"
	"fmt"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

const CurrentVersion = 2

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
	{from: 1, to: 2, name: "group-health-monitoring-v2", apply: migrateV1ToV2},
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

func migrateV1ToV2(root *yaml.Node) error {
	version := mappingValue(root, "config-version")
	if version == nil {
		return fmt.Errorf("v1 migration requires config-version")
	}
	remora := mappingValue(root, "remora")
	if remora != nil {
		if mappingValue(remora, "monitoring") != nil {
			return fmt.Errorf("remora.monitoring is reserved for config-version 2")
		}
		if err := renameKey(remora, "health-api-hearbeat", "health-api-heartbeat"); err != nil {
			return err
		}
		base := time.Second
		if node := mappingValue(remora, "heartbeat-interval"); node != nil {
			var duration Duration
			if err := node.Decode(&duration); err != nil {
				return fmt.Errorf("remora.heartbeat-interval: %w", err)
			}
			base = duration.Duration
		}
		if base <= 0 {
			return fmt.Errorf("remora.heartbeat-interval must be positive")
		}
		healthEvery, err := positiveInt(mappingValue(remora, "health-api-heartbeat"), 10)
		if err != nil {
			return fmt.Errorf("remora.health-api-heartbeat: %w", err)
		}
		failureThreshold, err := positiveInt(mappingValue(remora, "api-failure-threshold"), 3)
		if err != nil {
			return fmt.Errorf("remora.api-failure-threshold: %w", err)
		}

		monitoring := mappingNode()
		setMappingValue(monitoring, "interval", scalarNode(base.String()))
		api := mappingNode()
		setMappingValue(api, "interval", scalarNode((base * time.Duration(healthEvery)).String()))
		setMappingValue(api, "failure-threshold", intNode(failureThreshold))
		setMappingValue(monitoring, "jellyfin-api", api)

		if oldLogin := mappingValue(remora, "user-login-watchdog"); oldLogin != nil {
			login := cloneMappingWithout(oldLogin, "heartbeat")
			if heartbeat := mappingValue(oldLogin, "heartbeat"); heartbeat != nil {
				every, decodeErr := positiveInt(heartbeat, 0)
				if decodeErr != nil {
					return fmt.Errorf("remora.user-login-watchdog.heartbeat: %w", decodeErr)
				}
				setMappingValue(login, "interval", scalarNode((base * time.Duration(every)).String()))
			}
			setMappingValue(monitoring, "user-login", login)
		}

		for _, key := range []string{"heartbeat-interval", "health-api-heartbeat", "api-failure-threshold", "user-login-watchdog"} {
			removeMappingValue(remora, key)
		}
		setMappingValue(remora, "monitoring", monitoring)
	}
	version.Tag = "!!int"
	version.Value = "2"
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

func positiveInt(node *yaml.Node, fallback int) (int, error) {
	if node == nil {
		return fallback, nil
	}
	var value int
	if err := node.Decode(&value); err != nil || value < 1 {
		return 0, fmt.Errorf("must be a positive integer")
	}
	return value, nil
}

func mappingNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

func scalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func intNode(value int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(value)}
}

func setMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func removeMappingValue(mapping *yaml.Node, key string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

func cloneMappingWithout(mapping *yaml.Node, excluded ...string) *yaml.Node {
	clone := mappingNode()
	skip := make(map[string]bool, len(excluded))
	for _, key := range excluded {
		skip[key] = true
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if !skip[mapping.Content[i].Value] {
			clone.Content = append(clone.Content, mapping.Content[i], mapping.Content[i+1])
		}
	}
	return clone
}
