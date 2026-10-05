package gatekeeper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
)

// GroupCluster is the group of the Kubernetes tools. A tool of a group is offered only to runs that have it; a tool
// without a group is offered to every run with gatekeeper access.
const GroupCluster = "cluster"

// Tool is something an agent can call. Its arguments are validated by Decode, strictly: a tool never sees an argument
// that Decode did not return.
type Tool struct {
	Name        string
	Description string
	// Group says which runs are offered the tool: empty for every run with gatekeeper access, GroupCluster for runs
	// that were started with cluster tools. A run that is not offered a tool cannot call it either, and its attempt is
	// answered like a call of a tool that does not exist.
	Group string
	// Mutating tools change something and wait for the maintainer's approval before they run.
	Mutating bool
	// Schema is the JSON schema of the arguments, shown to the model by tools/list. It documents; Decode enforces.
	Schema json.RawMessage
	// Decode validates the arguments an agent sent and returns them in canonical form. That form is what is stored,
	// shown for approval and executed. A problem the agent can fix is an ArgumentError.
	Decode func(raw json.RawMessage) (json.RawMessage, error)
	// Check is an optional precondition of a mutating tool, run on the decoded arguments before an approval is asked
	// for: asking the maintainer to approve something that cannot work wastes their attention. Its error is shown to
	// the agent like an argument error.
	Check func(ctx context.Context, args json.RawMessage) error
	// Incident returns the incident a call with these (decoded) arguments is about, or 0. Optional.
	Incident func(args json.RawMessage) int64
	// Activity is optional and for a mutating tool: the text of the entry the activity log gets when the approved call
	// succeeded. The entry is written in the transaction that records the result, so an action is logged or it did not
	// happen.
	Activity func(args json.RawMessage) string
	// Run executes the tool with decoded arguments and returns the text for the model.
	Run func(ctx context.Context, c Call) (string, error)
}

// Call is what a tool gets to run with.
type Call struct {
	RunID  string
	CallID int64
	Args   json.RawMessage
	// RequestedAt is when the call was made, which for a mutating tool is when the approval was asked for. A tool can
	// tell with it whether the world changed after the question was asked.
	RequestedAt time.Time
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

// offeredTo says whether a run is offered the tool.
func (t Tool) offeredTo(r run.Run) bool {
	switch t.Group {
	case "":
		return true
	case GroupCluster:
		return r.Cluster
	}
	return false
}

var toolName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func validateTool(t Tool) error {
	switch {
	case !toolName.MatchString(t.Name):
		return fmt.Errorf("tool name %q is not lower case letters, digits and underscores", t.Name)
	case t.Decode == nil || t.Run == nil:
		return fmt.Errorf("tool %q needs Decode and Run", t.Name)
	case t.Group != "" && t.Group != GroupCluster:
		return fmt.Errorf("tool %q is in the group %q, which does not exist", t.Name, t.Group)
	}
	return nil
}
