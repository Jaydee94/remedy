package sshhost

import "time"

// SetCommandTimeout shortens the time a command may take, for a test. It returns the function that restores it. Tests that use it
// must not run in parallel.
func SetCommandTimeout(d time.Duration) (restore func()) {
	old := commandTimeout
	commandTimeout = d
	return func() { commandTimeout = old }
}
