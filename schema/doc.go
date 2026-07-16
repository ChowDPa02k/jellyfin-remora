// Package schema embeds the public compatibility manifest for validation by
// tooling and tests.
package schema

import _ "embed"

// CompatibilityManifest is the machine-readable v0.9 compatibility baseline.
//
//go:embed compatibility-v0.9.json
var CompatibilityManifest []byte
