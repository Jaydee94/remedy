package settings_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Jaydee94/remedy/internal/settings"
)

// The budgets are whole cents: the settings know no decimal kind, and the Setup page shows dollars.
func TestTheApiBudgetsHaveTheirDefaultsAndBounds(t *testing.T) {
	s, _ := newReader(t, nil)
	ctx := context.Background()
	if got := s.Int(settings.APIBudgetPerRunCents); got != 100 {
		t.Errorf("per run default = %d cents, want 100", got)
	}
	if got := s.Int(settings.APIBudgetPerDayCents); got != 500 {
		t.Errorf("per day default = %d cents, want 500", got)
	}
	for key, c := range map[string]struct {
		ok, bad []string
	}{
		settings.APIBudgetPerRunCents: {[]string{"5", "100", "2000"}, []string{"0", "4", "2001", "-1", `"1.00"`, "1.5"}},
		settings.APIBudgetPerDayCents: {[]string{"0", "500", "100000"}, []string{"-1", "100001", `"5"`, "2.5"}},
	} {
		for _, v := range c.ok {
			if _, err := s.Set(ctx, key, json.RawMessage(v)); err != nil {
				t.Errorf("%s = %s was refused: %v", key, v, err)
			}
		}
		for _, v := range c.bad {
			before := s.Int(key)
			if _, err := s.Set(ctx, key, json.RawMessage(v)); err == nil {
				t.Errorf("%s = %s was accepted", key, v)
			}
			if s.Int(key) != before {
				t.Errorf("%s changed to %d after a refused value", key, s.Int(key))
			}
		}
	}
}

func TestTheApiBudgetsAreGroupedAndNamedForTheEnvironment(t *testing.T) {
	s, _ := newReader(t, nil)
	for key, env := range map[string]string{
		settings.APIBudgetPerRunCents: "REMEDY_API_BUDGET_PER_RUN_CENTS",
		settings.APIBudgetPerDayCents: "REMEDY_API_BUDGET_PER_DAY_CENTS",
	} {
		e, ok := s.Entry(key)
		if !ok || e.Spec.Env != env || e.Spec.Group != "API key" || e.Spec.Label == "" || e.Spec.Rule == "" {
			t.Errorf("%s: entry %+v (found %v)", key, e.Spec, ok)
		}
	}
}
