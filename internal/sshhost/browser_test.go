package sshhost_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/Jaydee94/remedy/internal/sshhost/sshtest"
)

// TestServeForBrowser serves a test SSH server until the test is killed, so that a throwaway Remedy server can be pointed at it
// while the UI is checked in a browser. It does nothing unless REMEDY_BROWSER_SSH is set.
//
//	REMEDY_BROWSER_SSH=1 go test ./internal/sshhost -run TestServeForBrowser -v -timeout 0
func TestServeForBrowser(t *testing.T) {
	if os.Getenv("REMEDY_BROWSER_SSH") == "" {
		t.Skip("set REMEDY_BROWSER_SSH=1 to serve a test SSH server until the test is killed")
	}
	srv := sshtest.Start(t, sshtest.Config{Password: "browser-pw", Commands: okCommands()})
	fmt.Printf("SSH test server on %s:%d, password browser-pw\n", srv.Host, srv.Port)
	select {}
}
