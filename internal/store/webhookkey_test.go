package store_test

import (
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

func TestWebhookKeyPartsSplitsAtTheFirstSlash(t *testing.T) {
	for key, want := range map[string][2]string{
		"grafana/disk-full/nas": {"grafana", "disk-full/nas"},
		"a/b":                   {"a", "b"},
		"noslash":               {"", "noslash"},
		"":                      {"", ""},
	} {
		ep, rest := store.WebhookKeyParts(key)
		if ep != want[0] || rest != want[1] {
			t.Errorf("WebhookKeyParts(%q) = %q, %q, want %q, %q", key, ep, rest, want[0], want[1])
		}
	}
	if !store.IsOutageSource(store.SourceAlertmanager) || !store.IsOutageSource(store.SourceWebhook) || store.IsOutageSource(store.SourceGitHub) || store.IsOutageSource(store.SourceArgoCD) {
		t.Error("IsOutageSource names the wrong sources")
	}
}
