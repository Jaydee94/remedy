package loginrelay

import (
	"errors"
	"time"
)

// Errors are sentinels with fixed texts. None of them wraps the input that
// caused it, because that input may be the sign-in address or the code.
var (
	ErrBadURL     = errors.New("login: invalid sign-in address")
	ErrBadCode    = errors.New("login: invalid code")
	ErrBadBody    = errors.New("login: invalid request body")
	ErrNoSession  = errors.New("login: no such session")
	ErrWrongState = errors.New("login: the session is not in the right state")
)

// State is where a login session stands.
type State string

const (
	StateIdle         State = "idle"
	StateStarting     State = "starting"
	StateAwaitingCode State = "awaiting_code"
	StateSubmitting   State = "submitting"
	StateSucceeded    State = "succeeded"
	StateFailed       State = "failed"
	StateCancelled    State = "cancelled"
	StateExpired      State = "expired"
)

// Reason says why a session failed. Only a closed set of words ever leaves
// the relay.
type Reason string

const (
	ReasonNoURL           Reason = "no_url"
	ReasonBadURL          Reason = "bad_url"
	ReasonInvalidCode     Reason = "invalid_code"
	ReasonProcessExited   Reason = "process_exited"
	ReasonNotConfirmed    Reason = "not_confirmed"
	ReasonTimeout         Reason = "timeout"
	ReasonRunnerLost      Reason = "runner_lost"
	ReasonRunnerRestarted Reason = "runner_restarted"
	ReasonExpired         Reason = "expired"
	ReasonCancelled       Reason = "cancelled"
	ReasonRunActive       Reason = "run_active"
)

// Known reports whether r is one of the eleven reasons.
func (r Reason) Known() bool {
	switch r {
	case ReasonNoURL, ReasonBadURL, ReasonInvalidCode, ReasonProcessExited, ReasonNotConfirmed,
		ReasonTimeout, ReasonRunnerLost, ReasonRunnerRestarted, ReasonExpired, ReasonCancelled,
		ReasonRunActive:
		return true
	}
	return false
}

// FromRunner reports whether a runner may report r. The control plane alone
// decides cancelled, expired, runner_lost and runner_restarted.
func (r Reason) FromRunner() bool {
	switch r {
	case ReasonNoURL, ReasonBadURL, ReasonInvalidCode, ReasonProcessExited, ReasonNotConfirmed,
		ReasonTimeout, ReasonRunActive:
		return true
	}
	return false
}

// Refusal is the word of a 409 before a session exists.
type Refusal string

const (
	RefusalDisabled        Refusal = "disabled"
	RefusalRunnerOffline   Refusal = "runner_offline"
	RefusalRunActive       Refusal = "run_active"
	RefusalAlreadyActive   Refusal = "already_active"
	RefusalAPIKeyMode      Refusal = "api_key_mode"
	RefusalReplaceRequired Refusal = "replace_required"
)

// RefusedError says that a login was not started, and why.
type RefusedError struct{ Reason Refusal }

func (*RefusedError) Error() string { return "login: request refused" }

// RateLimitedError says that a limit was hit and when to try again.
type RateLimitedError struct{ RetryAfter time.Duration }

func (*RateLimitedError) Error() string { return "login: rate limited" }
