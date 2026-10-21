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

// GroupCluster is the group of the Kubernetes tools; GroupHost is the group of the host tools. A tool of a group is offered only to runs that have it; a tool
// without a group is offered to every run with gatekeeper access.
const GroupCluster = "cluster"

// GroupHost is the group of the host tools. They are offered only to runs that were started with hosts.
const GroupHost = "host"

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
	// CheckRun is Check with the run's id, for a tool whose precondition depends on the run (the hosts it was given). It
	// runs after Check, before an approval is asked for, and its error is shown to the agent like an argument error.
	CheckRun func(ctx context.Context, runID string, args json.RawMessage) error
	// Incident returns the incident a call with these (decoded) arguments is about, or 0. Optional.
	Incident func(args json.RawMessage) int64
	// Activity is optional and for a mutating tool: the text of the entry the activity log gets when the approved call
	// succeeded. The entry is written in the transaction that records the result, so an action is logged or it did not
	// happen.
	Activity func(args json.RawMessage) string
	// ActivityKind is the kind of the activity entry that Activity's text gets. Empty means store.KindClusterAction.
	ActivityKind string
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
	// Role is the role of the run, so that a tool can word its answer for the tools the run has (a learning run has no
	// host_change). It is set for a tool that does not mutate; an approved call runs without it.
	Role run.Role
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

// learnerTools are the only tools a learning run is offered: it reads one host and loads skills, and it can change nothing. The list is
// an allowlist: a tool that is added later is not offered to a learning run unless it is named here, and the incident and note tools
// (which have no group) are not.
var learnerTools = map[string]bool{"host_list": true, "host_read": true, "skill_get": true}

// offeredTo says whether a run is offered the tool. A learning run gets the tools of learnerTools and no other; for every other run
// the group of the tool decides.
func (t Tool) offeredTo(r run.Run) bool {
	if r.Role == run.RoleLearner && !learnerTools[t.Name] {
		return false
	}
	switch t.Group {
	case "":
		return true
	case GroupCluster:
		return r.Cluster
	case GroupHost:
		return r.HostCount > 0
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
	case t.Group != "" && t.Group != GroupCluster && t.Group != GroupHost:
		return fmt.Errorf("tool %q is in the group %q, which does not exist", t.Name, t.Group)
	}
	return nil
}
