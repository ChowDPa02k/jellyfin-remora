package kickstart

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:generate go run ./cmd/generate-localizations -url http://127.0.0.1:18096 -output localization.json

//go:embed localization.json
var localizationJSON []byte

type LocalizationCatalog struct {
	SourceVersion     string   `json:"source_version"`
	DisplayLanguages  []string `json:"display_languages"`
	MetadataLanguages []string `json:"metadata_languages"`
	MetadataRegions   []string `json:"metadata_regions"`
}

func Localizations() (LocalizationCatalog, error) {
	var catalog LocalizationCatalog
	if err := json.Unmarshal(localizationJSON, &catalog); err != nil {
		return LocalizationCatalog{}, fmt.Errorf("decode embedded Jellyfin localizations: %w", err)
	}
	if len(catalog.DisplayLanguages) == 0 || len(catalog.MetadataLanguages) == 0 || len(catalog.MetadataRegions) == 0 {
		return LocalizationCatalog{}, fmt.Errorf("embedded Jellyfin localization catalog is incomplete")
	}
	return catalog, nil
}
