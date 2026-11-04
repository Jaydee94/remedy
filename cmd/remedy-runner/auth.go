package main

import (
	"errors"
	"strings"
)

// minKeyLength is the shortest value taken for an API key; a real key has about a hundred characters.
const minKeyLength = 20

func keyShape(key string) bool {
	for i := 0; i < len(key); i++ {
		c := key[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// checkAuth says what the runner's mode needs from its environment, before anything starts. In key mode the variable
// ANTHROPIC_API_KEY must hold something (that it is a valid key is for the first run to find out: a bad key fails that run
// with a fixed sentence). In subscription mode a key in the environment is not used, and the warning says so without the value.
func checkAuth(mode string, getenv func(string) string) (warning string, err error) {
	raw := getenv("ANTHROPIC_API_KEY")
	key := strings.TrimSpace(raw)
	switch mode {
	case "api_key":
		if key == "" {
			return "", errors.New("REMEDY_AUTH_MODE=api_key needs ANTHROPIC_API_KEY in the runner's environment")
		}
		// The runner scrubs the key from everything it posts by its characters; a key outside this shape (or a Secret made
		// from a file that ends in a newline) would be handed to the CLI as it is and fail every run, or slip past the scrubber.
		if raw != key || len(key) < minKeyLength || !keyShape(key) {
			return "", errors.New("ANTHROPIC_API_KEY must be the key alone: at least 20 letters, digits, '-' or '_', with no space or line break around it")
		}
	default:
		if key != "" {
			return "ANTHROPIC_API_KEY is set but ignored: this runner uses the subscription login", nil
		}
	}
	return "", nil
}
