// Package sample exposes repository assets embedded in the release binaries.
// The source files remain ordinary files so users and packagers can inspect or
// distribute them independently.
package sample

import "embed"

// Files contains every YAML configuration template in this directory.
//
//go:embed *.yaml
var Files embed.FS

// SplashASCII is printed once when the Jellyfin Remora daemon starts.
//
//go:embed splash_ascii.txt
var SplashASCII []byte
