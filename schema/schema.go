// Package schema embeds the JSON Schema for scenario files so programs can
// use it without reading it from disk (the AI generator sends it to models
// to shape their output).
package schema

import _ "embed"

// Scenario is schema/scenario.schema.json.
//
//go:embed scenario.schema.json
var Scenario []byte
