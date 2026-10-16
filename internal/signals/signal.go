// Package signals turns what a monitoring source reports into incidents. A Source says, in one answer, everything that is wrong
// right now; the Runner asks every source once per poll interval, maps each signal to a host and gives it to the incident engine.
package signals

import (
	"context"
	"encoding/json"
	"time"
)

// Signal is one thing that is wrong, normalised and bounded. It is all the engine knows about a source.
type Signal struct {
	Key      string
	Title    string
	Severity string // critical, warning, info or none
	Auto     bool   // the responder may start on its own; decided once, when the incident opens
	Details  json.RawMessage
	// Labels are the labels the host rules are compared with. They are not stored (Details holds a bounded copy).
	Labels map[string]string
	URL    string
	SeenAt time.Time
}

// Source reports everything that is wrong right now. A source that cannot give a complete answer returns an error and nothing else:
// the runner then resolves nothing.
type Source interface {
	Name() string
	// Enabled says whether the source is set up. A source that is not is not asked.
	Enabled() bool
	Fetch(ctx context.Context) ([]Signal, error)
}
