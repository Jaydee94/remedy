//go:build webui

// Package web exposes the built UI (web/dist) to the control plane binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the built UI rooted at index.html.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is embedded at build time, so Sub cannot fail
	}
	return sub
}
