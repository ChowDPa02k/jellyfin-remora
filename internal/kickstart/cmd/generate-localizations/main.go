package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

type option struct {
	Name        string `json:"Name"`
	DisplayName string `json:"DisplayName"`
}

type publicInfo struct {
	Version string `json:"Version"`
}

type catalog struct {
	SourceVersion     string   `json:"source_version"`
	DisplayLanguages  []string `json:"display_languages"`
	MetadataLanguages []string `json:"metadata_languages"`
	MetadataRegions   []string `json:"metadata_regions"`
}

func main() {
	base := flag.String("url", "http://127.0.0.1:8096", "Jellyfin base URL")
	output := flag.String("output", "localization.json", "output JSON path")
	flag.Parse()
	client := &http.Client{Timeout: 10 * time.Second}
	var info publicInfo
	fetch(client, *base+"/System/Info/Public", &info)
	var display, languages, regions []option
	fetch(client, *base+"/Localization/Options", &display)
	fetch(client, *base+"/Localization/Cultures", &languages)
	fetch(client, *base+"/Localization/Countries", &regions)
	result := catalog{
		SourceVersion:     info.Version,
		DisplayLanguages:  labels(display, func(value option) string { return value.Name }),
		MetadataLanguages: labels(languages, func(value option) string { return value.DisplayName }),
		MetadataRegions:   labels(regions, func(value option) string { return value.DisplayName }),
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(*output, data, 0o644); err != nil {
		fatal(err)
	}
}

func fetch(client *http.Client, url string, output any) {
	response, err := client.Get(url)
	if err != nil {
		fatal(fmt.Errorf("GET %s: %w", url, err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		fatal(fmt.Errorf("GET %s: %s", url, response.Status))
	}
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		fatal(fmt.Errorf("decode %s: %w", url, err))
	}
}

func labels(values []option, value func(option) string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, item := range values {
		label := strings.TrimSpace(value(item))
		if label != "" && !seen[label] {
			seen[label] = true
			result = append(result, label)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return strings.ToLower(result[i]) < strings.ToLower(result[j]) })
	if len(result) == 0 {
		fatal(fmt.Errorf("Jellyfin returned an empty localization list"))
	}
	return result
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
