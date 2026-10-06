// Package packs embeds the built-in product packs and their catalogue.
package packs

import "embed"

// FS holds catalog.yaml and every shipped pack directory. Add a new
// shipped pack's directory to the embed list below.
//
//go:embed catalog.yaml ecommerce llm-apps identity public-apis chat
var FS embed.FS
