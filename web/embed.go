// Package web embeds the built panel UI (web/dist, produced by `npm run build` in web/).
package web

import "embed"

// Dist holds the compiled panel.
//
//go:embed all:dist
var Dist embed.FS
