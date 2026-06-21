// Package webui embeds the built React SPA (web/dist) into the Go binary.
//
// Run `npm --prefix web run build` before `go build` so dist/ exists.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the built SPA filesystem rooted at dist/.
func FS() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}
