package loginrelay

import (
	"testing"
	"time"
)

// gateOpen asks the claim gate and lets go of it at once.
func gateOpen(h *harness) bool {
	release, open := h.m.ClaimGate()
	release()
	return open
}

// A session the control plane ended while the runner holds its child keeps the
// claim gate shut until the runner is done with the cancel (a poll that no
// longer names the session), or for at most cancelGateCap.
func TestClaimGateWaitsForTheRunnersCancel(t *testing.T) {
	ends := map[string]func(t *testing.T, h *harness, id string){
		"cancel from the page": func(t *testing.T, h *harness, id string) {
			if err := h.m.Cancel(id); err != nil {
				t.Fatal(err)
			}
		},
		"expiry": func(t *testing.T, h *harness, id string) {
			// The runner keeps polling, so the TTL and not runner_lost ends it.
			for range 29 {
				h.clock.Advance(sessionTTL / 30)
				if c := h.poll(t, id, 1); c != nil {
					t.Fatalf("command = %+v, want none", c)
				}
			}
			h.clock.Advance(sessionTTL / 30)
			h.m.Tick()
			h.state(t, StateExpired, ReasonExpired)
		},
	}
	for name, end := range ends {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			id := driveToAwaiting(t, h)
			end(t, h, id)
			if gateOpen(h) {
				t.Fatal("the gate is open while the runner has not confirmed the cancel")
			}
			// The runner takes the cancel and stops its child; the gate stays shut.
			c := h.poll(t, id, 1)
			if c == nil || c.Action != ActionCancel {
				t.Fatalf("command = %+v, want a cancel", c)
			}
			if gateOpen(h) {
				t.Fatal("the gate is open while the runner is still stopping its child")
			}
			// The next poll no longer names the session: the child is gone.
			if c := h.poll(t, "", 0); c != nil {
				t.Fatalf("command = %+v, want none", c)
			}
			if !gateOpen(h) {
				t.Fatal("the gate is shut after the runner confirmed the cancel")
			}
		})
	}
}

func TestClaimGateCapsTheWaitForTheRunner(t *testing.T) {
	h := newHarness(t)
	id := driveToAwaiting(t, h)
	if err := h.m.Cancel(id); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(cancelGateCap - time.Second)
	if gateOpen(h) {
		t.Fatal("the gate is open before the cap")
	}
	h.clock.Advance(2 * time.Second)
	if !gateOpen(h) {
		t.Fatal("the gate is still shut after the cap")
	}
}

func TestClaimGateIgnoresASessionWithoutAChild(t *testing.T) {
	t.Run("the runner never took it", func(t *testing.T) {
		h := newHarness(t)
		id := h.start(t)
		if err := h.m.Cancel(id); err != nil {
			t.Fatal(err)
		}
		if !gateOpen(h) {
			t.Fatal("the gate is shut for a session without a child")
		}
	})
	t.Run("the runner ended it itself", func(t *testing.T) {
		h := newHarness(t)
		id := driveToAwaiting(t, h)
		if err := h.m.SubmitCode(id, NewMaterial(plantedCode)); err != nil {
			t.Fatal(err)
		}
		wantCommand(t, h.poll(t, id, 1), ActionCode, 2, id)
		h.report(t, Report{Session: id, State: string(StateSucceeded), Attempt: 1})
		if !gateOpen(h) {
			t.Fatal("the gate is shut after the runner's own report")
		}
	})
}
