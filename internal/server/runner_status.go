package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"
)

// runnerGrace is how long after its last authenticated request the runner counts as connected. The runner claims every
// 25 seconds at most and reports every 15, so a connected runner is never silent this long.
const runnerGrace = 45 * time.Second

// maxVersionLen bounds the CLI's version line a runner may report.
const maxVersionLen = 64

// RunnerStatus is what the control plane knows about its runner: when it last made an authenticated request, and what it
// last said about the CLI's login. It is held in memory: a restart forgets it and the runner reports again within seconds.
type RunnerStatus struct {
	mu         sync.Mutex
	lastSeen   time.Time
	login      string
	checkedAt  time.Time
	cliVersion string
}

func NewRunnerStatus() *RunnerStatus { return &RunnerStatus{login: "unknown"} }

// Touch notes an authenticated request of the runner. A nil *RunnerStatus does nothing.
func (r *RunnerStatus) Touch(now time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.lastSeen = now
	r.mu.Unlock()
}

// Report records what the runner says. It refuses a login word it does not know and a version line that is not a
// short line of printable ASCII, and replaces a checked-at time that is zero, in the future or older than a day by now.
func (r *RunnerStatus) Report(login string, checkedAt time.Time, cliVersion string, now time.Time) error {
	switch login {
	case "ok", "missing", "unknown":
	default:
		return errors.New("login must be ok, missing or unknown")
	}
	if len(cliVersion) > maxVersionLen {
		return errors.New("cliVersion is too long")
	}
	for _, c := range cliVersion {
		if c < ' ' || c > '~' {
			return errors.New("cliVersion must be printable ASCII")
		}
	}
	if checkedAt.IsZero() || checkedAt.After(now.Add(time.Minute)) || checkedAt.Before(now.Add(-24*time.Hour)) {
		checkedAt = now
	}
	r.mu.Lock()
	r.lastSeen, r.login, r.checkedAt, r.cliVersion = now, login, checkedAt, cliVersion // a report is a request of the runner too
	r.mu.Unlock()
	return nil
}

// RunnerView is what GET /api/runner answers.
type RunnerView struct {
	Connected      bool       `json:"connected"`
	LastSeenAt     *time.Time `json:"lastSeenAt,omitempty"`
	Login          string     `json:"login"` // ok, missing or unknown; unknown whenever the runner is not connected
	LoginCheckedAt *time.Time `json:"loginCheckedAt,omitempty"`
	CLIVersion     string     `json:"cliVersion,omitempty"`
}

// View is the status as of now. A runner that is not connected has an unknown login, whatever it said last: a stale
// report must never say "logged in".
func (r *RunnerStatus) View(now time.Time) RunnerView {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := RunnerView{Login: "unknown", CLIVersion: r.cliVersion}
	if !r.lastSeen.IsZero() {
		seen := r.lastSeen
		v.LastSeenAt = &seen
		v.Connected = now.Sub(seen) <= runnerGrace
	}
	if !r.checkedAt.IsZero() {
		checked := r.checkedAt
		v.LoginCheckedAt = &checked
	}
	if v.Connected {
		v.Login = r.login
	}
	return v
}

// postRunnerStatus is POST /runner/v1/status, behind the runner token.
func (s *srv) postRunnerStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Login          string    `json:"login"`
		LoginCheckedAt time.Time `json:"loginCheckedAt"`
		CLIVersion     string    `json:"cliVersion"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid status")
		return
	}
	if err := s.d.RunnerStatus.Report(body.Login, body.LoginCheckedAt, body.CLIVersion, time.Now()); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getRunner is GET /api/runner, behind the admin session.
func (s *srv) getRunner(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.d.RunnerStatus.View(time.Now()))
}
