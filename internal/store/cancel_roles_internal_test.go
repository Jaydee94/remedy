package store

import (
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
)

// Which roles CancelRun accepts is an allowlist: a role added later is not cancellable until somebody decides it is. runs.role has a
// CHECK that admits only the three roles of today, so a run of another role cannot be stored; this test asks the allowlist directly.
func TestOnlyAnAdHocOrALearningRunMayBeCancelled(t *testing.T) {
	for _, c := range []struct {
		role run.Role
		want bool
	}{
		{run.RoleAdhoc, true},
		{run.RoleLearner, true},
		{run.RoleResponder, false},
		{run.Role("fixer"), false},
		{run.Role(""), false},
	} {
		if got := cancellableRole(c.role); got != c.want {
			t.Errorf("cancellableRole(%q) = %v, want %v", c.role, got, c.want)
		}
	}
}
