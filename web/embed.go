// Package web embeds the built UI.
package web

import "embed"

// Dist holds the production build of the web UI (web/dist), served by the
// Stampede server. Rebuild it with `pnpm build` in this directory.
//
//go:embed all:dist
var Dist embed.FS
