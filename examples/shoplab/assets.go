// Package shoplab holds the files ShopLab embeds into its binary: the SQL
// migrations and the OpenAPI document served at /openapi.yaml.
package shoplab

import "embed"

// Migrations contains migrations/NNNN_name.sql, applied in lexical order.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// OpenAPI is the OpenAPI 3.1 description of the HTTP API.
//
//go:embed openapi.yaml
var OpenAPI []byte
