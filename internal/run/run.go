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

type Run struct {
	ID         string     `json:"id"`
	Provider   string     `json:"provider"`
	Prompt     string     `json:"prompt"`
	Status     Status     `json:"status"`
	ExitCode   *int       `json:"exitCode,omitempty"`
	Result     string     `json:"result"`
	SessionID  string     `json:"sessionId"`
	CostUSD    float64    `json:"costUsd"`
	CreatedAt  time.Time  `json:"createdAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

type Event struct {
	Seq       int             `json:"seq"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"createdAt"`
}

// Outcome is what the runner reports when a run has ended.
type Outcome struct {
	ExitCode  int     `json:"exitCode"`
	Result    string  `json:"result"`
	SessionID string  `json:"sessionId"`
	CostUSD   float64 `json:"costUsd"`
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
