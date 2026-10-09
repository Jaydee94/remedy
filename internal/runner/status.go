package runner

import (
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Jaydee94/remedy/internal/provider"
)

// Status is what the runner knows about itself: whether the control plane answers, whether the CLI is logged in, and
// the CLI's version line. The claim loop, the login checker and the reporter share it. Every method is safe for
// concurrent use, and a nil *Status does nothing and says "unknown": a runner without one (the older tests) needs no
// checks.
type Status struct {
	mu         sync.Mutex
	connected  bool
	login      provider.LoginState
	checkedAt  time.Time
	cliVersion string
}

func NewStatus() *Status { return &Status{login: provider.LoginUnknown} }

// SetConnected records whether the control plane answered the runner's last request.
func (s *Status) SetConnected(ok bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.connected = ok
	s.mu.Unlock()
}

func (s *Status) Connected() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

// SetLogin records what the last login check found and when.
func (s *Status) SetLogin(state provider.LoginState, at time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.login, s.checkedAt = state, at
	s.mu.Unlock()
}

func (s *Status) SetCLIVersion(v string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.cliVersion = v
	s.mu.Unlock()
}

// Report is what the runner tells the control plane: the body of POST /runner/v1/status. It carries a state and a version
// line, never output of the CLI.
type Report struct {
	Login          string    `json:"login"`
	LoginCheckedAt time.Time `json:"loginCheckedAt"`
	CLIVersion     string    `json:"cliVersion,omitempty"`
}

func (s *Status) Report() Report {
	if s == nil {
		return Report{Login: string(provider.LoginUnknown)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return Report{Login: string(s.login), LoginCheckedAt: s.checkedAt, CLIVersion: s.cliVersion}
}

// StatusHandler serves the probes of the runner pod. /livez answers 200 whenever the process serves HTTP. /readyz
// answers 200 once the control plane has answered the runner and 503 after a failed claim or report. The login never
// decides readiness: a cluster without a login must still become ready.
func StatusHandler(s *Status) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !s.Connected() {
			http.Error(w, "the control plane has not answered", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "connected\n")
	})
	return mux
}
