package gatekeeper_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/internal/testutil"
)

// When the store cannot expire a call at its deadline (here: a trigger refuses the update), the waiter must try
// again instead of giving up: the call would stay pending for good, the run's clock paused and the runner held.
func TestAnExpiryThatFailsIsRetriedUntilItWorks(t *testing.T) {
	e, in := automaticEnv(t, ms(100))
	db := testutil.RawDB(t, e.dsn)
	if _, err := db.Exec(`CREATE FUNCTION refuse_expiry_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'refused for the test'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER refuse_expiry BEFORE UPDATE ON tool_calls FOR EACH ROW WHEN (NEW.status = 'abandoned') EXECUTE FUNCTION refuse_expiry_fn()`); err != nil {
		t.Fatal(err)
	}

	s := e.open(t, "toolu_1", "incident_add_note", noteArgs(in.ID, "a note"))
	// The deadline passes while every attempt fails: the call is still waiting.
	time.Sleep(500 * time.Millisecond)
	if c := e.pending(t); c.Status != store.CallWaiting {
		t.Fatalf("call = %+v, want it waiting while the store refuses", c)
	}
	if _, err := db.Exec(`DROP TRIGGER refuse_expiry ON tool_calls`); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	text, isErr := s.result()
	if !isErr || !strings.Contains(text, "no decision within") {
		t.Fatalf("result = %q (error %v), want the expiry error", text, isErr)
	}
	if took := time.Since(start); took > 4*time.Second {
		t.Fatalf("the expiry took %v after the store recovered", took)
	}
	if n := expiredEntries(t, e); n != 1 {
		t.Fatalf("activity %v: want one %s", activityKinds(t, e.st), store.KindApprovalExpired)
	}
}
