package server

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spa serves a built single-page app: existing files as-is, everything else as index.html.
// Unknown /api/, /runner/ and /mcp paths stay 404 instead of returning the HTML shell.
func spa(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/runner/") ||
			r.URL.Path == "/mcp" || strings.HasPrefix(r.URL.Path, "/mcp/") {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if f, err := fsys.Open(name); err == nil {
			_ = f.Close()
			files.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		files.ServeHTTP(w, r)
	})
}
