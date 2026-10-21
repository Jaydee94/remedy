package loginrelay

import (
	"errors"
	"testing"
	"time"
)

func TestReasonVocabulary(t *testing.T) {
	known := []string{
		"no_url", "bad_url", "invalid_code", "process_exited", "not_confirmed", "timeout",
		"runner_lost", "runner_restarted", "expired", "cancelled", "run_active",
	}
	for _, w := range known {
		if !Reason(w).Known() {
			t.Errorf("%q should be known", w)
		}
	}
	notRunner := map[string]bool{"cancelled": true, "expired": true, "runner_lost": true, "runner_restarted": true}
	for _, w := range known {
		if got, want := Reason(w).FromRunner(), !notRunner[w]; got != want {
			t.Errorf("%q FromRunner = %v, want %v", w, got, want)
		}
	}
	for _, w := range []string{"", "OK", "No_Url", "timeout "} {
		if Reason(w).Known() || Reason(w).FromRunner() {
			t.Errorf("%q should be unknown", w)
		}
	}
}

func TestStateAndRefusalWords(t *testing.T) {
	states := map[State]string{
		StateIdle: "idle", StateStarting: "starting", StateAwaitingCode: "awaiting_code",
		StateSubmitting: "submitting", StateSucceeded: "succeeded", StateFailed: "failed",
		StateCancelled: "cancelled", StateExpired: "expired",
	}
	for s, w := range states {
		if string(s) != w {
			t.Errorf("state %q != %q", s, w)
		}
	}
	refusals := map[Refusal]string{
		RefusalDisabled: "disabled", RefusalRunnerOffline: "runner_offline", RefusalRunActive: "run_active",
		RefusalAlreadyActive: "already_active", RefusalAPIKeyMode: "api_key_mode", RefusalReplaceRequired: "replace_required",
	}
	for r, w := range refusals {
		if string(r) != w {
			t.Errorf("refusal %q != %q", r, w)
		}
	}
}

func TestFixedErrorTexts(t *testing.T) {
	var err error = &RefusedError{Reason: RefusalDisabled}
	var re *RefusedError
	if !errors.As(err, &re) || re.Reason != RefusalDisabled {
		t.Fatal("RefusedError does not unwrap")
	}
	if err.Error() == "" {
		t.Fatal("empty text")
	}
	err = &RateLimitedError{RetryAfter: time.Minute}
	var rl *RateLimitedError
	if !errors.As(err, &rl) || rl.RetryAfter != time.Minute {
		t.Fatal("RateLimitedError does not unwrap")
	}
	if err.Error() == "" {
		t.Fatal("empty text")
	}
	for _, e := range []error{ErrBadURL, ErrBadCode, ErrBadBody, ErrNoSession, ErrWrongState} {
		if e == nil || e.Error() == "" {
			t.Fatal("sentinel missing")
		}
	}
}
