package main

import (
	"errors"
	"strings"
)

// checkAuth says what the runner's mode needs from its environment, before anything starts. In key mode the variable
// ANTHROPIC_API_KEY must hold something (that it is a valid key is for the first run to find out: a bad key fails that run
// with a fixed sentence). In subscription mode a key in the environment is not used, and the warning says so without the value.
func checkAuth(mode string, getenv func(string) string) (warning string, err error) {
	key := strings.TrimSpace(getenv("ANTHROPIC_API_KEY"))
	switch mode {
	case "api_key":
		if key == "" {
			return "", errors.New("REMEDY_AUTH_MODE=api_key needs ANTHROPIC_API_KEY in the runner's environment")
		}
	default:
		if key != "" {
			return "ANTHROPIC_API_KEY is set but ignored: this runner uses the subscription login", nil
		}
	}
	return "", nil
}
