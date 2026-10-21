package runner

import (
	"time"

	"github.com/Jaydee94/remedy/internal/loginrelay"
)

// SetOnCodeWritten hands the tests the code holder right after the relay wrote it to the CLI and zeroed it.
func SetOnCodeWritten(l *LoginRelay, f func(loginrelay.Material)) { l.onCodeWritten = f }

// SetExitedWinsTheCodeWait makes the watcher's wait for the code see only the child's exit: before that wait it lets
// the code be written and the child exit, then hides the closed code channel. This is the ordering in which Go's select
// may pick the exit although the code was written. A step that does not happen within 5 s is not waited for further.
func SetExitedWinsTheCodeWait(l *LoginRelay) {
	l.beforeCodeWait = func(ch *loginChild) {
		limit := time.After(5 * time.Second)
		for _, c := range []chan struct{}{ch.code, ch.exited} {
			select {
			case <-c:
			case <-limit:
				return
			}
		}
		ch.code = make(chan struct{}) // never closed: only the exit is ready
	}
}
