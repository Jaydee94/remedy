// Package run holds the domain types shared by the control plane and the runner.
package run

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

type Status string

const (
	Queued    Status = "queued"
	Running   Status = "running"
	Succeeded Status = "succeeded"
	Failed    Status = "failed"
)

// Terminal reports whether no further state change is expected.
func (s Status) Terminal() bool { return s == Succeeded || s == Failed }

// Role says who a run is for.
type Role string

const (
	// RoleAdhoc is a run the maintainer started by hand.
	RoleAdhoc Role = "adhoc"
	// RoleResponder is a run that diagnoses an incident.
	RoleResponder Role = "responder"
)

// Failure reasons. A runner can only report ReasonTimeout; ReasonInvalidOutput is set by the control
// plane when a responder's answer does not match the diagnosis schema.
const (
	// ReasonTimeout is the failure reason of a run that was stopped for taking too long, by the runner
	// or by the control plane's reaper.
	ReasonTimeout = "timeout"
	// ReasonInvalidOutput means the agent finished but its structured answer was missing or invalid.
	ReasonInvalidOutput = "invalid_output"
	// ReasonCancelled means the maintainer cancelled the run. A runner reports it after stopping the agent.
	ReasonCancelled = "cancelled"
)

type Run struct {
	ID              string          `json:"id"`
	Provider        string          `json:"provider"`
	Prompt          string          `json:"prompt"`
	Status          Status          `json:"status"`
	ExitCode        *int            `json:"exitCode,omitempty"`
	Result          string          `json:"result"`
	SessionID       string          `json:"sessionId"`
	CostUSD         float64         `json:"costUsd"`
	CreatedAt       time.Time       `json:"createdAt"`
	StartedAt       *time.Time      `json:"startedAt,omitempty"`
	FinishedAt      *time.Time      `json:"finishedAt,omitempty"`
	Role            Role            `json:"role"`
	IncidentID      *int64          `json:"incidentId,omitempty"`
	Output          json.RawMessage `json:"output,omitempty"`
	FailureReason   string          `json:"failureReason,omitempty"`
	HeadSHA         string          `json:"headSha,omitempty"`
	Automatic       bool            `json:"automatic,omitempty"`
	MCP             bool            `json:"mcp,omitempty"`             // the run has access to the gatekeeper
	CancelRequested bool            `json:"cancelRequested,omitempty"` // the maintainer cancelled the run
}

type Event struct {
	Seq       int             `json:"seq"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"createdAt"`
}

// Outcome is what the runner reports when a run has ended.
type Outcome struct {
	ExitCode      int             `json:"exitCode"`
	Result        string          `json:"result"`
	SessionID     string          `json:"sessionId"`
	CostUSD       float64         `json:"costUsd"`
	Output        json.RawMessage `json:"output,omitempty"`
	FailureReason string          `json:"failureReason,omitempty"`
}

// Claim is what the control plane answers to a runner that claims a run: the run itself and what a
// responder run needs besides it.
type Claim struct {
	Run
	// Schema is the JSON schema the agent must answer with. It is empty for a run with a free-text answer.
	Schema json.RawMessage `json:"schema,omitempty"`
	// Snapshot is true when the runner must download the repository snapshot of the run before it starts.
	Snapshot bool `json:"snapshot,omitempty"`
	// MCPToken is the token of a run with gatekeeper access: the bearer token for the MCP endpoint of the control
	// plane. It exists only in this answer; the control plane keeps a hash.
	MCPToken string `json:"mcp_token,omitempty"`
}

// NewID returns a random 128-bit identifier as 32 hex characters.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing is unrecoverable
	}
	return hex.EncodeToString(b)
}

// JSONString encodes s as a JSON string value.
func JSONString(s string) json.RawMessage {
	b, _ := json.Marshal(s) // marshalling a string cannot fail
	return b
}
