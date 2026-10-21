package server

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/Jaydee94/remedy/internal/loginrelay"
)

// maxLoginBody bounds the bodies of the start and cancel routes; the code route has its own limit in DecodeCodeRequest.
const maxLoginBody = 1 << 10

// refusalSentence is the fixed sentence of each refusal word. The page maps the word to its own sentence; this one is for
// a reader of the API.
var refusalSentence = map[loginrelay.Refusal]string{
	loginrelay.RefusalDisabled:        "The login from this page is turned off.",
	loginrelay.RefusalRunnerOffline:   "The runner is not connected, so it cannot start a login.",
	loginrelay.RefusalRunActive:       "A run is running. Start the login when it has ended.",
	loginrelay.RefusalAlreadyActive:   "A login is already in progress.",
	loginrelay.RefusalAPIKeyMode:      "The runner uses an API key, so there is no login to start.",
	loginrelay.RefusalReplaceRequired: "The runner is logged in already. Confirm that you want to log in again.",
}

// noStore marks every answer of a login route as not to be cached, errors included. It wraps the guards, so a 401 or a 403
// carries the header too.
func noStore(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next(w, r)
	}
}

// loginSessionJSON is the session as the admin API shows it. URL is set only while awaiting_code; there is no field for a
// code. It is never logged.
type loginSessionJSON struct {
	ID          string    `json:"id"`
	State       string    `json:"state"`
	Attempt     int       `json:"attempt"`
	URL         string    `json:"url,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	ExpiresAt   time.Time `json:"expiresAt"`
	WaitingRuns int       `json:"waitingRuns"`
}

func sessionJSON(v loginrelay.View, waiting int) *loginSessionJSON {
	out := &loginSessionJSON{
		ID: v.ID, State: string(v.State), Attempt: v.Attempt, Reason: string(v.Reason),
		ExpiresAt: v.ExpiresAt.UTC(), WaitingRuns: waiting,
	}
	if v.State == loginrelay.StateAwaitingCode && v.URL.Len() > 0 {
		out.URL = string(v.URL.Bytes())
	}
	return out
}

// writeLoginJSON writes an answer that may hold the sign-in address and clears its own buffer afterwards.
func writeLoginJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not write the answer")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
	clear(b)
}

// decodeLoginBody decodes one JSON object of at most 1 KiB into v: unknown fields and anything after the object are refused.
func decodeLoginBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

func writeRefusal(w http.ResponseWriter, reason loginrelay.Refusal) {
	writeJSON(w, http.StatusConflict, map[string]string{"error": refusalSentence[reason], "reason": string(reason)})
}

func writeRateLimited(w http.ResponseWriter, after time.Duration) {
	secs := int(math.Ceil(after.Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeErr(w, http.StatusTooManyRequests, "too many attempts, try again later")
}

// waitingRuns is how many runs wait for the session to end.
func (s *srv) waitingRuns(r *http.Request) (int, error) {
	_, queued, err := s.d.Store.RunCounts(r.Context())
	return queued, err
}

// getLogin is GET /api/runner/login: whether a login can be started here, and the session, if there is one.
func (s *srv) getLogin(w http.ResponseWriter, r *http.Request) {
	v := s.d.Login.View()
	defer v.URL.Zero()
	body := struct {
		Available   bool              `json:"available"`
		Unavailable string            `json:"unavailable"`
		Session     *loginSessionJSON `json:"session"`
	}{Available: true}
	if s.d.RunnerStatus.AuthMode() != AuthModeSubscription {
		body.Available, body.Unavailable = false, string(loginrelay.RefusalAPIKeyMode)
	}
	if v.State != loginrelay.StateIdle {
		waiting, err := s.waitingRuns(r)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not count the runs")
			return
		}
		body.Session = sessionJSON(v, waiting)
	}
	writeLoginJSON(w, http.StatusOK, body)
}

// startLogin is POST /api/runner/login.
func (s *srv) startLogin(w http.ResponseWriter, r *http.Request) {
	if s.d.RunnerStatus.AuthMode() != AuthModeSubscription {
		writeRefusal(w, loginrelay.RefusalAPIKeyMode)
		return
	}
	var req struct {
		Replace bool `json:"replace"`
	}
	if !decodeLoginBody(w, r, &req) {
		return
	}
	loggedIn := s.d.RunnerStatus != nil && s.d.RunnerStatus.View(time.Now()).Login == "ok"
	v, err := s.d.Login.Start(r.Context(), req.Replace, loggedIn)
	var refused *loginrelay.RefusedError
	var limited *loginrelay.RateLimitedError
	switch {
	case errors.As(err, &refused):
		writeRefusal(w, refused.Reason)
		return
	case errors.As(err, &limited):
		writeRateLimited(w, limited.RetryAfter)
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "could not check the runs")
		return
	}
	defer v.URL.Zero()
	waiting, err := s.waitingRuns(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not count the runs")
		return
	}
	writeLoginJSON(w, http.StatusCreated, sessionJSON(v, waiting))
}

// submitLoginCode is POST /api/runner/login/code. The code is never echoed: every error is a fixed sentence.
func (s *srv) submitLoginCode(w http.ResponseWriter, r *http.Request) {
	id, code, err := loginrelay.DecodeCodeRequest(r.Body)
	if errors.Is(err, loginrelay.ErrBadCode) {
		writeErr(w, http.StatusBadRequest, "invalid code")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// On success the manager owns the code and zeroes it itself; on a refusal it is ours to zero.
	err = s.d.Login.SubmitCode(id, code)
	if err == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	code.Zero()
	var limited *loginrelay.RateLimitedError
	switch {
	case errors.As(err, &limited):
		writeRateLimited(w, limited.RetryAfter)
	case errors.Is(err, loginrelay.ErrBadCode):
		writeErr(w, http.StatusBadRequest, "invalid code")
	default:
		writeErr(w, http.StatusConflict, "This login is not waiting for a code.")
	}
}

// cancelLogin is POST /api/runner/login/cancel.
func (s *srv) cancelLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Session string `json:"session"`
	}
	if !decodeLoginBody(w, r, &req) {
		return
	}
	if err := s.d.Login.Cancel(req.Session); err != nil {
		writeErr(w, http.StatusConflict, "There is no open login with this id.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pollLogin is POST /runner/v1/login/poll: the runner's long poll for a command.
func (s *srv) pollLogin(w http.ResponseWriter, r *http.Request) {
	req, err := loginrelay.DecodePoll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cmd, err := s.d.Login.Poll(r.Context(), req)
	if err != nil {
		return // the runner went away
	}
	if cmd == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	b := cmd.WireJSON()
	cmd.Code.Zero()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
	clear(b)
}

// reportLogin is POST /runner/v1/login/report: the runner's state report. The manager judges the address.
func (s *srv) reportLogin(w http.ResponseWriter, r *http.Request) {
	rep, err := loginrelay.DecodeReport(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.d.Login.Report(rep); err != nil {
		writeErr(w, http.StatusConflict, "the login session is unknown or not in this state")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
