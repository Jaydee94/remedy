package sshhost

import "time"

// SetConnectTimeout shortens the time a connection may take, for a test. It returns the function that restores it. Tests that
// use it must not run in parallel.
func SetConnectTimeout(d time.Duration) (restore func()) {
	old := connectTimeout
	connectTimeout = d
	return func() { connectTimeout = old }
}

// SetCommandTimeout shortens the time a command may take, for a test. It returns the function that restores it. Tests that use it
// must not run in parallel.
func SetCommandTimeout(d time.Duration) (restore func()) {
	old := commandTimeout
	commandTimeout = d
	return func() { commandTimeout = old }
}
