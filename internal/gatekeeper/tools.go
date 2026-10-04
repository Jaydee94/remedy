package gatekeeper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
)

// Tool is something an agent can call. Its arguments are validated by Decode, strictly: a tool never sees an argument
// that Decode did not return.
type Tool struct {
	Name        string
	Description string
	// Mutating tools change something and wait for the maintainer's approval before they run.
	Mutating bool
	// Schema is the JSON schema of the arguments, shown to the model by tools/list. It documents; Decode enforces.
	Schema json.RawMessage
	// Decode validates the arguments an agent sent and returns them in canonical form. That form is what is stored,
	// shown for approval and executed. A problem the agent can fix is an ArgumentError.
	Decode func(raw json.RawMessage) (json.RawMessage, error)
	// Incident returns the incident a call with these (decoded) arguments is about, or 0. Optional.
	Incident func(args json.RawMessage) int64
	// Run executes the tool with decoded arguments and returns the text for the model.
	Run func(ctx context.Context, c Call) (string, error)
}

// Call is what a tool gets to run with.
type Call struct {
	RunID  string
	CallID int64
	Args   json.RawMessage
}

// ArgumentError is an error the agent can fix: its text is shown to the agent. Any other error of a tool is shown as
// "the tool failed" and logged, so that internal details do not reach the model.
type ArgumentError string

func (e ArgumentError) Error() string { return string(e) }

// DecodeArgs decodes the arguments of a call strictly: no unknown members, no trailing data. Empty arguments mean {}.
func DecodeArgs(raw json.RawMessage, v any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return ArgumentError("invalid arguments: " + err.Error())
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return ArgumentError("invalid arguments: unexpected data after the object")
	}
	return nil
}

var toolName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func validateTool(t Tool) error {
	switch {
	case !toolName.MatchString(t.Name):
		return fmt.Errorf("tool name %q is not lower case letters, digits and underscores", t.Name)
	case t.Decode == nil || t.Run == nil:
		return fmt.Errorf("tool %q needs Decode and Run", t.Name)
	}
	return nil
}
