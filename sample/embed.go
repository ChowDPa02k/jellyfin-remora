// Package sample exposes the platform configuration templates embedded in
// remoractl. The YAML files remain ordinary repository files so users and
// packagers can inspect or distribute them independently.
package sample

import "embed"

// Files contains every YAML configuration template in this directory.
//
//go:embed *.yaml
var Files embed.FS
