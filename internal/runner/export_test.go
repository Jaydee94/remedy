package runner

import "github.com/Jaydee94/remedy/internal/loginrelay"

// SetOnCodeWritten hands the tests the code holder right after the relay wrote it to the CLI and zeroed it.
func SetOnCodeWritten(l *LoginRelay, f func(loginrelay.Material)) { l.onCodeWritten = f }
