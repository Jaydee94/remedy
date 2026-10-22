package store_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
)

func hostFields(name string) store.HostFields {
	return store.HostFields{
		Name: name, Address: "192.168.178.118", Port: 22, Account: "remedy", Notes: "the NAS",
		DockerPattern: "remedy-*", SystemdUnits: []string{"node_exporter.service"}, Enabled: true,
	}
}

func activitySummaries(t *testing.T, s *store.Store) []string {
	t.Helper()
	log, err := s.ListActivity(context.Background(), store.ActivityQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, a := range log {
		out = append(out, a.Summary)
	}
	return out
}

func TestCreateGetListHost(t *testing.T) {
	s, ctx := openStore(t), context.Background()

	nas, err := s.CreateHost(ctx, hostFields("nas"))
	if err != nil {
		t.Fatal(err)
	}
	if nas.ID == 0 || nas.Status != store.HostNew || nas.Port != 22 || !nas.Enabled || nas.PinnedKey != "" || nas.Report != "" {
		t.Fatalf("created host = %+v", nas)
	}
	if len(nas.SystemdUnits) != 1 || nas.SystemdUnits[0] != "node_exporter.service" {
		t.Fatalf("units = %v", nas.SystemdUnits)
	}

	// A name that differs only in case is the same name.
	if _, err := s.CreateHost(ctx, hostFields("NAS")); !errors.Is(err, store.ErrExists) {
		t.Fatalf("duplicate name error = %v, want ErrExists", err)
	}
	if _, err := s.CreateHost(ctx, hostFields("alpha")); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListHosts(ctx)
	if err != nil || len(list) != 2 || list[0].Host.Name != "alpha" || list[1].Host.Name != "nas" {
		t.Fatalf("list = %+v, err = %v", list, err)
	}
	if list[0].Credential != nil {
		t.Fatalf("a host without a login has a credential: %+v", list[0].Credential)
	}
	if _, err := s.GetHost(ctx, 999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown host error = %v", err)
	}
	if got := activitySummaries(t, s); len(got) != 2 || got[1] != "Host nas created" {
		t.Fatalf("activity = %v", got)
	}
}

// The order is the one SQLite's NOCASE column gave: the names folded to lower case, then compared byte by byte, whatever the
// database's collation (en_US would ignore the hyphen and put nas-2 last).
func TestListHostsOrdersByTheFoldedBytesOfTheName(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	for _, name := range []string{"nas10", "NAS1", "nas-2", "Beta", "alpha"} {
		if _, err := s.CreateHost(ctx, hostFields(name)); err != nil {
			t.Fatalf("CreateHost(%s): %v", name, err)
		}
	}
	list, err := s.ListHosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range list {
		got = append(got, e.Host.Name)
	}
	want := []string{"alpha", "Beta", "nas-2", "NAS1", "nas10"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestUpdateHostClearsThePinWhenTheEndpointChanges(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	h, _ := s.CreateHost(ctx, hostFields("nas"))
	if err := s.PinHostKey(ctx, h.ID, "ssh-ed25519 AAAA", time.Now()); err != nil {
		t.Fatal(err)
	}

	// Notes and account do not touch the pin.
	f := hostFields("nas")
	f.Notes = "changed"
	if _, cleared, err := s.UpdateHost(ctx, h.ID, f); err != nil || cleared {
		t.Fatalf("notes change: cleared = %v, err = %v", cleared, err)
	}
	if e, _ := s.GetHost(ctx, h.ID); e.Host.PinnedKey == "" {
		t.Fatal("a notes change cleared the pin")
	}

	// A new address belongs to another machine until the maintainer says otherwise.
	f.Address = "192.168.178.200"
	got, cleared, err := s.UpdateHost(ctx, h.ID, f)
	if err != nil || !cleared {
		t.Fatalf("address change: cleared = %v, err = %v", cleared, err)
	}
	if got.PinnedKey != "" || got.PinnedAt != nil || got.Status != store.HostUntrusted {
		t.Fatalf("host after an address change = %+v", got)
	}
}

func TestPinUnpinAndTestStatus(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	h, _ := s.CreateHost(ctx, hostFields("nas"))

	at := time.Now().UTC()
	if err := s.SaveHostTest(ctx, h.ID, "", store.HostOK, "", `{"account":"remedy"}`, at); err != nil {
		t.Fatal(err)
	}
	e, _ := s.GetHost(ctx, h.ID)
	if e.Host.Status != store.HostOK || e.Host.Report != `{"account":"remedy"}` || e.Host.CheckedAt == nil {
		t.Fatalf("after a test = %+v", e.Host)
	}

	if err := s.PinHostKey(ctx, h.ID, "ssh-ed25519 AAAA", at); err != nil {
		t.Fatal(err)
	}
	e, _ = s.GetHost(ctx, h.ID)
	if e.Host.PinnedKey != "ssh-ed25519 AAAA" || e.Host.PinnedAt == nil || e.Host.Status != store.HostNew {
		t.Fatalf("after pinning = %+v", e.Host)
	}
	if err := s.UnpinHostKey(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	e, _ = s.GetHost(ctx, h.ID)
	if e.Host.PinnedKey != "" || e.Host.Status != store.HostUntrusted {
		t.Fatalf("after unpinning = %+v", e.Host)
	}
	if err := s.PinHostKey(ctx, 999, "ssh-ed25519 AAAA", at); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("pin on an unknown host = %v", err)
	}
}

func TestHostCredentialAndCascade(t *testing.T) {
	s, dsn := openStoreAt(t)
	ctx := context.Background()
	h, _ := s.CreateHost(ctx, hostFields("nas"))

	cred := store.HostCredential{HostID: h.ID, Kind: "key", Ciphertext: []byte{1, 2, 3}, Hint: "SHA256:abc", PublicKey: "ssh-ed25519 AAAA", UpdatedAt: time.Now()}
	if err := s.SaveHostCredential(ctx, store.HostCredential{HostID: 999, Kind: "key", Ciphertext: []byte{1}}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("credential for an unknown host = %v", err)
	}
	if err := s.SaveHostCredential(ctx, cred); err != nil {
		t.Fatal(err)
	}
	cred.Kind, cred.Hint = "password", "set" // a second save replaces the first
	if err := s.SaveHostCredential(ctx, cred); err != nil {
		t.Fatal(err)
	}
	e, _ := s.GetHost(ctx, h.ID)
	if e.Credential == nil || e.Credential.Kind != "password" || e.Credential.Hint != "set" || !bytes.Equal(e.Credential.Ciphertext, []byte{1, 2, 3}) {
		t.Fatalf("credential = %+v", e.Credential)
	}

	// Deleting the host deletes its credential.
	if err := s.DeleteHost(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := rawDB(t, dsn).QueryRow(`SELECT COUNT(*) FROM host_credentials`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("credentials left after deleting the host: %d, %v", n, err)
	}
	if err := s.DeleteHost(ctx, h.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleting twice = %v", err)
	}
}

func TestDeleteHostCredentialAndUndecryptableReset(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	h, _ := s.CreateHost(ctx, hostFields("nas"))
	_ = s.SaveHostCredential(ctx, store.HostCredential{HostID: h.ID, Kind: "password", Ciphertext: []byte{9}, Hint: "set", UpdatedAt: time.Now()})
	_ = s.SaveHostTest(ctx, h.ID, "", store.HostUndecryptable, "cannot decrypt", "", time.Now())

	// A new login replaces what the master key could not open, so the host is untested again.
	_ = s.SaveHostCredential(ctx, store.HostCredential{HostID: h.ID, Kind: "password", Ciphertext: []byte{8}, Hint: "set", UpdatedAt: time.Now()})
	if e, _ := s.GetHost(ctx, h.ID); e.Host.Status != store.HostNew || e.Host.StatusDetail != "" {
		t.Fatalf("after replacing an undecryptable login = %+v", e.Host)
	}
	if err := s.DeleteHostCredential(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.GetHost(ctx, h.ID); e.Credential != nil {
		t.Fatal("the credential is still there")
	}
	if err := s.DeleteHostCredential(ctx, h.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleting a missing credential = %v", err)
	}
}

func TestSealedCredentialIsBoundToItsHost(t *testing.T) {
	key, err := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := key.Seal([]byte(`{"v":1}`), store.HostCredentialAAD(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := key.Open(sealed, store.HostCredentialAAD(1)); err != nil {
		t.Fatalf("opening with the right host: %v", err)
	}
	if _, err := key.Open(sealed, store.HostCredentialAAD(2)); !errors.Is(err, secret.ErrOpen) {
		t.Fatalf("opening for another host = %v, want ErrOpen", err)
	}
	// The value is part of every sealed credential in every database: this test pins it.
	if got := store.HostCredentialAAD(12); got != "host_credential:12" {
		t.Fatalf("HostCredentialAAD(12) = %q", got)
	}
}

func TestHostActivityNamesNoSecrets(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	h, _ := s.CreateHost(ctx, hostFields("nas"))
	_ = s.SaveHostCredential(ctx, store.HostCredential{HostID: h.ID, Kind: "key", Ciphertext: []byte{1}, Hint: "SHA256:abc", UpdatedAt: time.Now()})
	_ = s.PinHostKey(ctx, h.ID, "ssh-ed25519 AAAA", time.Now())
	_ = s.DeleteHost(ctx, h.ID)

	want := []string{"Host nas removed", "Host key of nas pinned", "Credential of nas replaced", "Host nas created"}
	got := activitySummaries(t, s)
	if len(got) != len(want) {
		t.Fatalf("activity = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("activity[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestChangingTheLoginMakesAnOkHostUntestedAgain(t *testing.T) {
	cred := store.HostCredential{Kind: "password", Ciphertext: []byte{9}, Hint: "set", UpdatedAt: time.Now()}
	for _, st := range []store.HostStatus{store.HostOK, store.HostError, store.HostUndecryptable} {
		for _, op := range []string{"replace", "delete"} {
			s, ctx := openStore(t), context.Background()
			h, _ := s.CreateHost(ctx, hostFields("nas"))
			cred.HostID = h.ID
			if err := s.SaveHostCredential(ctx, cred); err != nil {
				t.Fatal(err)
			}
			if err := s.SaveHostTest(ctx, h.ID, "", st, "detail", `{"account":"remedy"}`, time.Now()); err != nil {
				t.Fatal(err)
			}
			if op == "replace" {
				err := s.SaveHostCredential(ctx, cred)
				if err != nil {
					t.Fatal(err)
				}
			} else if err := s.DeleteHostCredential(ctx, h.ID); err != nil {
				t.Fatal(err)
			}
			e, _ := s.GetHost(ctx, h.ID)
			if e.Host.Status != store.HostNew || e.Host.Report != "" || e.Host.StatusDetail != "" {
				t.Fatalf("%s a login on a %s host: %+v", op, st, e.Host)
			}
		}
	}
}

func TestChangingTheLoginKeepsUntrustedAndKeyChanged(t *testing.T) {
	cred := store.HostCredential{Kind: "password", Ciphertext: []byte{9}, Hint: "set", UpdatedAt: time.Now()}
	for _, st := range []store.HostStatus{store.HostUntrusted, store.HostKeyChanged} {
		for _, op := range []string{"replace", "delete"} {
			s, ctx := openStore(t), context.Background()
			h, _ := s.CreateHost(ctx, hostFields("nas"))
			cred.HostID = h.ID
			_ = s.SaveHostCredential(ctx, cred)
			_ = s.SaveHostTest(ctx, h.ID, "", st, "detail", "", time.Now())
			if op == "replace" {
				_ = s.SaveHostCredential(ctx, cred)
			} else {
				_ = s.DeleteHostCredential(ctx, h.ID)
			}
			if e, _ := s.GetHost(ctx, h.ID); e.Host.Status != st || e.Host.StatusDetail != "detail" {
				t.Fatalf("%s a login on a %s host: %+v", op, st, e.Host)
			}
		}
	}
}

func TestSaveHostTestDropsAResultForAChangedPin(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	h, _ := s.CreateHost(ctx, hostFields("nas"))
	const key = "ssh-ed25519 AAAA"
	if err := s.PinHostKey(ctx, h.ID, key, time.Now()); err != nil {
		t.Fatal(err)
	}
	// The test ran against the pin; the pin is removed while it was in flight.
	if err := s.UnpinHostKey(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetHost(ctx, h.ID)
	err := s.SaveHostTest(ctx, h.ID, key, store.HostOK, "", `{"account":"remedy"}`, time.Now())
	if !errors.Is(err, store.ErrStale) {
		t.Fatalf("stale result error = %v, want ErrStale", err)
	}
	after, _ := s.GetHost(ctx, h.ID)
	if after.Host.Status != before.Host.Status || after.Host.Report != "" || after.Host.CheckedAt != nil {
		t.Fatalf("a stale result changed the row: %+v", after.Host)
	}
	// A pin that was replaced is stale as well.
	_ = s.PinHostKey(ctx, h.ID, "ssh-ed25519 BBBB", time.Now())
	if err := s.SaveHostTest(ctx, h.ID, key, store.HostOK, "", "{}", time.Now()); !errors.Is(err, store.ErrStale) {
		t.Fatalf("replaced pin error = %v, want ErrStale", err)
	}
	if err := s.SaveHostTest(ctx, h.ID, "ssh-ed25519 BBBB", store.HostOK, "", "{}", time.Now()); err != nil {
		t.Fatalf("a result for the current pin: %v", err)
	}
	if err := s.SaveHostTest(ctx, 999, "", store.HostOK, "", "{}", time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown host = %v, want ErrNotFound", err)
	}
}

func TestTheHostRowNoticesEveryChangeThatCouldMakeAnApprovalStale(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	h, _ := s.CreateHost(ctx, hostFields("nas"))
	updated := func() time.Time {
		t.Helper()
		e, err := s.GetHost(ctx, h.ID)
		if err != nil {
			t.Fatal(err)
		}
		return e.Host.UpdatedAt
	}
	last := updated()
	step := func(name string, change func() error, moves bool) {
		t.Helper()
		time.Sleep(3 * time.Millisecond)
		if err := change(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		now := updated()
		if moves && !now.After(last) {
			t.Errorf("%s: updated_at did not move (%v -> %v)", name, last, now)
		}
		if !moves && !now.Equal(last) {
			t.Errorf("%s: updated_at moved (%v -> %v), a test result must not invalidate an approval", name, last, now)
		}
		last = now
	}
	cred := store.HostCredential{HostID: h.ID, Kind: "password", Ciphertext: []byte{1}, Hint: "set"}
	step("saving a login", func() error { return s.SaveHostCredential(ctx, cred) }, true)
	step("pinning a key", func() error { return s.PinHostKey(ctx, h.ID, "ssh-ed25519 AAAA", time.Now()) }, true)
	step("saving a test result", func() error {
		return s.SaveHostTest(ctx, h.ID, "ssh-ed25519 AAAA", store.HostOK, "", `{"sudo":"limited"}`, time.Now())
	}, false)
	step("unpinning a key", func() error { return s.UnpinHostKey(ctx, h.ID) }, true)
	step("removing the login", func() error { return s.DeleteHostCredential(ctx, h.ID) }, true)
}

func TestAHostKeepsItsAlertRules(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	f := hostFields("nas")
	f.Matchers = "a=\"b\""
	h, err := s.CreateHost(ctx, f)
	if err != nil || h.Matchers != "a=\"b\"" {
		t.Fatalf("CreateHost = %+v, %v", h, err)
	}
	f.Matchers = "instance=~\"nas.*\"\nsite=\"home\""
	if _, _, err := s.UpdateHost(ctx, h.ID, f); err != nil {
		t.Fatal(err)
	}
	e, err := s.GetHost(ctx, h.ID)
	if err != nil || e.Host.Matchers != f.Matchers {
		t.Fatalf("GetHost = %q, %v", e.Host.Matchers, err)
	}
	list, err := s.ListHosts(ctx)
	if err != nil || len(list) != 1 || list[0].Host.Matchers != f.Matchers {
		t.Fatalf("ListHosts = %+v, %v", list, err)
	}
}
