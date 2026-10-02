//go:build !webui

// Package web exposes the built UI (web/dist) to the control plane binary.
package web

import "io/fs"

// FS returns nil unless the binary is built with -tags webui.
func FS() fs.FS { return nil }
