//go:build !webui

package web_test

import (
	"testing"

	"github.com/Jaydee94/remedy/web"
)

func TestFSIsNilWithoutTheWebuiTag(t *testing.T) {
	if got := web.FS(); got != nil {
		t.Fatalf("FS() = %v, want nil so that plain go build/vet work without a built UI", got)
	}
}
