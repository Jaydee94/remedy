package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

func webhookFields() store.WebhookFields {
	return store.WebhookFields{Enabled: true, ResolveAfter: 24 * time.Hour, RatePerMinute: 60, NewPerHour: 30}
}

func TestAnIncidentOfTheSourceWebhookCanBeStored(t *testing.T) {
	s := openStore(t)
	in, err := s.OpenIncident(context.Background(), store.NewIncident{
		Source: store.SourceWebhook, Key: "ep/disk-full", Title: "Disk full", Severity: "warning", Details: []byte(`{}`), Conclusion: "firing",
	}, store.NewActivity{Kind: store.KindIncidentOpened, Summary: "opened"})
	if err != nil {
		t.Fatalf("a webhook incident was refused: %v", err)
	}
	if in.Source != "webhook" || in.RepoID != 0 {
		t.Fatalf("incident = %+v", in)
	}
	repo := seedRepo(t, s)
	_, err = s.OpenIncident(context.Background(), store.NewIncident{
		Source: store.SourceWebhook, Key: "ep/x", Title: "x", RepoID: repo.ID, Conclusion: "firing", Details: []byte(`{}`),
	}, store.NewActivity{Kind: store.KindIncidentOpened, Summary: "opened"})
	if err == nil {
		t.Fatal("a webhook incident with a repository was accepted")
	}
}

func TestCreateWebhookReturnsTheTokenOnceAndStoresOnlyItsHash(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	e, token, err := s.CreateWebhook(ctx, "grafana", webhookFields())
	if err != nil {
		t.Fatal(err)
	}
	if len(token) < 40 || e.Name != "grafana" || !e.Enabled || e.AutoDiagnose {
		t.Fatalf("endpoint = %+v, token length %d", e, len(token))
	}
	list, err := s.ListWebhooks(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	auth, err := s.WebhookByName(ctx, "grafana")
	if err != nil {
		t.Fatal(err)
	}
	if !store.TokenMatches(auth.TokenHash, token) || store.TokenMatches(auth.TokenHash, token+"x") {
		t.Fatal("TokenMatches does not tell the token from another")
	}
	if strings.Contains(string(auth.TokenHash), token) {
		t.Fatal("the stored value holds the token")
	}
}

func TestAWebhookNameFollowsTheRule(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	for _, bad := range []string{"", "-a", "A", "has space", "a_b", strings.Repeat("a", 41), "ünï"} {
		if _, _, err := s.CreateWebhook(ctx, bad, webhookFields()); !errors.Is(err, store.ErrBadWebhook) {
			t.Errorf("name %q: error = %v, want ErrBadWebhook", bad, err)
		}
	}
	if _, _, err := s.CreateWebhook(ctx, strings.Repeat("a", 40), webhookFields()); err != nil {
		t.Errorf("a name of 40 characters: %v", err)
	}
	if _, _, err := s.CreateWebhook(ctx, "grafana", webhookFields()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateWebhook(ctx, "grafana", webhookFields()); !errors.Is(err, store.ErrExists) {
		t.Errorf("a second endpoint of the same name: error = %v, want ErrExists", err)
	}
}

func TestTheLimitsAndTheSilenceLimitAreBounded(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	for name, mod := range map[string]func(*store.WebhookFields){
		"resolve after 1 minute":  func(f *store.WebhookFields) { f.ResolveAfter = time.Minute },
		"negative resolve after":  func(f *store.WebhookFields) { f.ResolveAfter = -time.Second },
		"rate zero":               func(f *store.WebhookFields) { f.RatePerMinute = 0 },
		"rate over 6000":          func(f *store.WebhookFields) { f.RatePerMinute = 6001 },
		"new per hour zero":       func(f *store.WebhookFields) { f.NewPerHour = 0 },
		"new per hour over 1000":  func(f *store.WebhookFields) { f.NewPerHour = 1001 },
		"resolve after not whole": func(f *store.WebhookFields) { f.ResolveAfter = 10*time.Minute + time.Millisecond },
	} {
		f := webhookFields()
		mod(&f)
		if _, _, err := s.CreateWebhook(ctx, "ep", f); !errors.Is(err, store.ErrBadWebhook) {
			t.Errorf("%s: error = %v, want ErrBadWebhook", name, err)
		}
	}
	f := webhookFields()
	f.ResolveAfter = 0 // explicit resolve only
	if _, _, err := s.CreateWebhook(ctx, "never", f); err != nil {
		t.Errorf("resolve after 0: %v", err)
	}
	f.ResolveAfter = 5 * time.Minute
	if _, _, err := s.CreateWebhook(ctx, "five", f); err != nil {
		t.Errorf("resolve after 5 minutes: %v", err)
	}
}

func TestRotationKeepsThePreviousTokenForTwentyFourHours(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	e, old, err := s.CreateWebhook(ctx, "grafana", webhookFields())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	rotated, fresh, err := s.RotateWebhookToken(ctx, e.ID, now)
	if err != nil || fresh == old {
		t.Fatalf("rotate: %v, new token equals the old: %v", err, fresh == old)
	}
	if rotated.PreviousValidUntil == nil || !rotated.PreviousValidUntil.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("PreviousValidUntil = %v, want %v", rotated.PreviousValidUntil, now.Add(24*time.Hour))
	}
	auth, err := s.WebhookByName(ctx, "grafana")
	if err != nil {
		t.Fatal(err)
	}
	if !store.TokenMatches(auth.TokenHash, fresh) || !store.TokenMatches(auth.PreviousTokenHash, old) || store.TokenMatches(auth.TokenHash, old) {
		t.Fatal("after a rotation the new token is current and the old one is the previous one")
	}
	if _, _, err := s.RotateWebhookToken(ctx, 999, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("rotating an unknown endpoint: %v", err)
	}
}

func TestDeletingAnEndpointKeepsItsIncidents(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	e, _, err := s.CreateWebhook(ctx, "grafana", webhookFields())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenIncident(ctx, store.NewIncident{Source: store.SourceWebhook, Key: "grafana/a", Title: "a", Conclusion: "firing", Details: []byte(`{}`)},
		store.NewActivity{Kind: store.KindIncidentOpened, Summary: "opened"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteWebhook(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WebhookByName(ctx, "grafana"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a deleted endpoint is still found: %v", err)
	}
	if _, err := s.FindActiveIncidentByKey(ctx, store.SourceWebhook, "grafana/a"); err != nil {
		t.Errorf("the incident went with the endpoint: %v", err)
	}
	if err := s.DeleteWebhook(ctx, e.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleting twice: %v", err)
	}
}

func TestUpdateWebhookChangesTheFieldsAndKeepsTheToken(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	e, token, err := s.CreateWebhook(ctx, "grafana", webhookFields())
	if err != nil {
		t.Fatal(err)
	}
	f := webhookFields()
	f.Enabled, f.AutoDiagnose, f.ResolveAfter = false, true, time.Hour
	u, err := s.UpdateWebhook(ctx, e.ID, f)
	if err != nil || u.Enabled || !u.AutoDiagnose || u.ResolveAfter != time.Hour {
		t.Fatalf("update = %+v, %v", u, err)
	}
	auth, _ := s.WebhookByName(ctx, "grafana")
	if !store.TokenMatches(auth.TokenHash, token) {
		t.Error("an update changed the token")
	}
	f.RatePerMinute = 0
	if _, err := s.UpdateWebhook(ctx, e.ID, f); !errors.Is(err, store.ErrBadWebhook) {
		t.Errorf("update with a bad rate: %v", err)
	}
}

func TestTouchWebhookWritesAtMostOncePerMinute(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	e, _, _ := s.CreateWebhook(ctx, "grafana", webhookFields())
	t0 := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	if err := s.TouchWebhook(ctx, e.ID, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchWebhook(ctx, e.ID, t0.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListWebhooks(ctx)
	if list[0].LastUsedAt == nil || !list[0].LastUsedAt.Equal(t0) {
		t.Fatalf("LastUsedAt = %v, want %v (the second touch came within a minute)", list[0].LastUsedAt, t0)
	}
	if err := s.TouchWebhook(ctx, e.ID, t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ListWebhooks(ctx)
	if !list[0].LastUsedAt.Equal(t0.Add(2 * time.Minute)) {
		t.Fatalf("LastUsedAt = %v", list[0].LastUsedAt)
	}
}

// Switching an endpoint off must end the rotation overlap: otherwise a token that leaked, rotated away and then "switched off" works
// again as soon as the endpoint is switched back on within the day.
func TestSwitchingAnEndpointOffEndsTheOverlapOfThePreviousToken(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	e, old, _ := s.CreateWebhook(ctx, "grafana", webhookFields())
	if _, _, err := s.RotateWebhookToken(ctx, e.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	f := webhookFields()
	f.Enabled = false
	if _, err := s.UpdateWebhook(ctx, e.ID, f); err != nil {
		t.Fatal(err)
	}
	f.Enabled = true
	u, err := s.UpdateWebhook(ctx, e.ID, f)
	if err != nil || u.PreviousValidUntil != nil {
		t.Fatalf("after off and on: %+v, %v", u, err)
	}
	auth, _ := s.WebhookByName(ctx, "grafana")
	if len(auth.PreviousTokenHash) != 0 || store.TokenMatches(auth.PreviousTokenHash, old) {
		t.Fatal("the previous token works again after the endpoint was switched back on")
	}
}

func TestChangingOtherFieldsKeepsTheOverlap(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	e, old, _ := s.CreateWebhook(ctx, "grafana", webhookFields())
	s.RotateWebhookToken(ctx, e.ID, time.Now())
	f := webhookFields()
	f.AutoDiagnose = true
	if _, err := s.UpdateWebhook(ctx, e.ID, f); err != nil {
		t.Fatal(err)
	}
	auth, _ := s.WebhookByName(ctx, "grafana")
	if !store.TokenMatches(auth.PreviousTokenHash, old) {
		t.Fatal("an unrelated change ended the overlap")
	}
}
