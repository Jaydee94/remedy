//go:build webui

package web_test

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/web"
)

// Run with: make web-build && go test -tags webui ./web/
func TestFSServesTheBuiltUIFromItsRoot(t *testing.T) {
	fsys := web.FS()
	if fsys == nil {
		t.Fatal("FS() = nil with the webui tag")
	}

	index, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		t.Fatalf("index.html must be at the root of the embedded FS: %v", err)
	}
	if !strings.Contains(string(index), `<div id="root">`) {
		t.Fatalf("index.html is not the built UI shell: %.120s", index)
	}
}
