package store_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
)

// PostgreSQL refuses a text value with a NUL byte or invalid UTF-8; SQLite stored it. Untrusted text (a pod log, host output,
// an agent's answer or note) can hold either, so the store's wrapper replaces them with U+FFFD before a statement is sent,
// and the write succeeds. These tests go through the public methods that the findings named.

const fffd = "�"

func TestFinishRunWithANULInTheResultCompletesTheRun(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r := claimedToolRun(t, s)
	if err := s.FinishRun(ctx, r.ID, run.Outcome{Result: "a\x00b\xffc", SessionID: "s\x00"}); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	got, err := s.GetRun(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != run.Succeeded || got.Result != "a"+fffd+"b"+fffd+"c" {
		t.Fatalf("run = status %s result %q", got.Status, got.Result)
	}
}

func TestFinishToolCallOfAReadToolWithNULAndInvalidUTF8(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r := claimedToolRun(t, s)
	call, _, err := s.BeginToolCall(ctx, newCall(r.ID, "t1", "cluster_pod_logs", store.CallKindRead))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishToolCall(ctx, call.ID, store.CallSucceeded, "log\x00line \xc3\x28", ""); err != nil {
		t.Fatalf("FinishToolCall: %v", err)
	}
	got, err := s.GetToolCall(ctx, call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.CallSucceeded || got.Result != "log"+fffd+"line "+fffd+"(" {
		t.Fatalf("call = status %s result %q", got.Status, got.Result)
	}

	failed, _, _ := s.BeginToolCall(ctx, newCall(r.ID, "t2", "host_read", store.CallKindRead))
	if err := s.FinishToolCall(ctx, failed.ID, store.CallFailed, "", "err\x00"); err != nil {
		t.Fatalf("FinishToolCall (failed): %v", err)
	}
	if got, _ := s.GetToolCall(ctx, failed.ID); got.Error != "err"+fffd {
		t.Fatalf("error = %q", got.Error)
	}
}

func TestAFinishedActionWithANULWritesItsActivityEntry(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r := claimedToolRun(t, s)
	call, _, _ := s.BeginToolCall(ctx, newCall(r.ID, "t1", "host_change", store.CallKindRead))
	if err := s.FinishToolCallWithActivityKind(ctx, call.ID, "restarted\x00", "Ran docker restart x\x00 on nas", store.KindHostAction); err != nil {
		t.Fatalf("FinishToolCallWithActivityKind: %v", err)
	}
	got, _ := s.GetToolCall(ctx, call.ID)
	if got.Status != store.CallSucceeded || got.Result != "restarted"+fffd {
		t.Fatalf("call = %+v", got)
	}
	log, err := s.ListActivity(ctx, store.ActivityQuery{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, a := range log {
		if a.Kind == store.KindHostAction && a.Summary == "Ran docker restart x"+fffd+" on nas" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no host action entry with the cleaned summary: %+v", log)
	}
}

func TestAddNoteWithNULAndInvalidUTF8(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	in := openAlert(t, s, "note/1", false)
	if err := s.AddNote(ctx, in.ID, "", "agent\x00 note \xff"); err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	notes, err := s.ListNotes(ctx, in.ID)
	if err != nil || len(notes) != 1 || notes[0].Note != "agent"+fffd+" note "+fffd {
		t.Fatalf("notes = %+v, %v", notes, err)
	}
	log, _ := s.ListActivity(ctx, store.ActivityQuery{Limit: 50})
	var found bool
	for _, a := range log {
		if a.Kind == store.KindNoteAdded && strings.HasSuffix(a.Summary, "agent"+fffd+" note "+fffd) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no note entry with the cleaned text: %+v", log)
	}
}

func TestAnActivitySummaryWithANUL(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	if _, err := s.OpenIncident(ctx, store.NewIncident{
		Source: store.SourceAlertmanager, Key: "k\x00", Title: "title\x00\xfe", Severity: "warning", Conclusion: "firing",
	}, store.NewActivity{Kind: store.KindIncidentOpened, Summary: "opened\x00"}); err != nil {
		t.Fatalf("OpenIncident: %v", err)
	}
	log, _ := s.ListActivity(ctx, store.ActivityQuery{Limit: 50})
	if len(log) == 0 || log[0].Summary != "opened"+fffd {
		t.Fatalf("activity = %+v", log)
	}
}

func TestARunEventPayloadWithInvalidUTF8(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r := claimedToolRun(t, s)
	// A JSON text may hold invalid UTF-8 inside a string (json.Valid accepts it); a raw NUL would not be JSON.
	payload := json.RawMessage("{\"line\":\"a\xffb\"}")
	if _, err := s.AppendEvent(ctx, r.ID, "stdout", payload); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	evs, err := s.Events(ctx, r.ID, 0)
	if err != nil || len(evs) != 1 {
		t.Fatalf("events = %+v, %v", evs, err)
	}
	if string(evs[0].Payload) != `{"line":"a`+fffd+`b"}` || !json.Valid(evs[0].Payload) {
		t.Fatalf("payload = %q", evs[0].Payload)
	}
}

// Bytes are never cleaned: a sealed token with a NUL and bytes that are not UTF-8 goes into its BYTEA column and comes back
// unchanged.
func TestBytesWithANULRoundTripThroughABytea(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	raw := []byte{0x00, 0xff, 'a', 0x00, 0xc3, 0x28, 0xfe}
	if err := s.SaveConnection(ctx, store.Connection{TokenCiphertext: raw, TokenHint: "…abcd", Status: store.ConnectionStatus("ok"), CheckedAt: time.Now()}); err != nil {
		t.Fatalf("SaveConnection: %v", err)
	}
	got, err := s.GetConnection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.TokenCiphertext, raw) {
		t.Fatalf("bytea came back as %x, want %x", got.TokenCiphertext, raw)
	}
}

// A sealed host credential still opens after a write and a read.
func TestASealedCredentialStillOpensAfterTheStore(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	key, err := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.CreateHost(ctx, hostFields("nas"))
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("-----BEGIN KEY-----\x00\xff-----END KEY-----")
	var sealed []byte
	// Seal until the ciphertext holds a NUL byte, so the test covers one for sure (each seal draws a fresh nonce).
	for i := 0; i < 1000; i++ {
		sealed, err = key.Seal(plain, store.HostCredentialAAD(h.ID))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.IndexByte(sealed, 0) >= 0 {
			break
		}
	}
	if bytes.IndexByte(sealed, 0) < 0 {
		t.Fatal("no ciphertext with a NUL byte in 1000 seals")
	}
	if err := s.SaveHostCredential(ctx, store.HostCredential{HostID: h.ID, Kind: "key", Ciphertext: sealed, Hint: "…key"}); err != nil {
		t.Fatalf("SaveHostCredential: %v", err)
	}
	e, err := s.GetHost(ctx, h.ID)
	if err != nil || e.Credential == nil {
		t.Fatalf("GetHost = %+v, %v", e, err)
	}
	if !bytes.Equal(e.Credential.Ciphertext, sealed) {
		t.Fatal("the sealed credential changed on its way through the store")
	}
	opened, err := key.Open(e.Credential.Ciphertext, store.HostCredentialAAD(h.ID))
	if err != nil || !bytes.Equal(opened, plain) {
		t.Fatalf("Open = %q, %v", opened, err)
	}
}
