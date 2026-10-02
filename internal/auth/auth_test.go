package auth_test

import (
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
)

func TestVerify(t *testing.T) {
	a := auth.New("correct horse battery")
	if !a.Verify("correct horse battery") {
		t.Fatal("correct password rejected")
	}
	if a.Verify("wrong") || a.Verify("") {
		t.Fatal("wrong password accepted")
	}
}

func TestSessions(t *testing.T) {
	a := auth.New("correct horse battery")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a.SetClock(func() time.Time { return now })

	tok := a.NewSession()
	if len(tok) < 40 {
		t.Fatalf("token too short: %q", tok)
	}
	if !a.ValidSession(tok) {
		t.Fatal("fresh session invalid")
	}
	if a.ValidSession("nope") || a.ValidSession("") {
		t.Fatal("unknown token accepted")
	}

	now = now.Add(auth.SessionTTL + time.Second)
	if a.ValidSession(tok) {
		t.Fatal("expired session accepted")
	}

	now = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	tok2 := a.NewSession()
	a.EndSession(tok2)
	if a.ValidSession(tok2) {
		t.Fatal("ended session accepted")
	}
}

func TestLoginRateLimit(t *testing.T) {
	a := auth.New("correct horse battery")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a.SetClock(func() time.Time { return now })

	for i := 0; i < 5; i++ {
		if a.Locked("1.2.3.4") {
			t.Fatalf("locked after only %d failures", i)
		}
		a.RecordFailure("1.2.3.4")
	}
	if !a.Locked("1.2.3.4") {
		t.Fatal("not locked after 5 failures")
	}
	if a.Locked("5.6.7.8") {
		t.Fatal("lock leaked to another key")
	}

	now = now.Add(11 * time.Minute)
	if a.Locked("1.2.3.4") {
		t.Fatal("still locked after the window passed")
	}

	a.RecordFailure("9.9.9.9")
	a.ResetFailures("9.9.9.9")
	for i := 0; i < 4; i++ {
		a.RecordFailure("9.9.9.9")
	}
	if a.Locked("9.9.9.9") {
		t.Fatal("ResetFailures did not clear the counter")
	}
}
