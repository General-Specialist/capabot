// Package webui provides the embedded static files for the Capabot web UI.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed dist
var distFS embed.FS

// FS returns a filesystem rooted at the web/dist directory.
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("webui: failed to sub dist: " + err.Error())
	}
	return sub
}
