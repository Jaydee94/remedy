package settings

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

func newCancelTestSettings(t *testing.T) *Settings {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(context.Background(), st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// A request that is cancelled after the write has committed must not leave the snapshot behind the table.
func TestSetAndDeleteReloadEvenWhenTheContextIsCancelledAfterTheCommit(t *testing.T) {
	s := newCancelTestSettings(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.afterCommit = cancel
	if _, err := s.Set(ctx, K8sWriteNamespaces, json.RawMessage(`["a"]`)); err != nil {
		t.Fatalf("Set after a cancel past the commit: %v", err)
	}
	if e, _ := s.Entry(K8sWriteNamespaces); e.Source != SourceSet || len(e.Value.List) != 1 || e.Value.List[0] != "a" {
		t.Fatalf("snapshot after Set = %+v, want the stored list [a]", e)
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	s.afterCommit = cancel2
	if _, err := s.Delete(ctx2, K8sWriteNamespaces); err != nil {
		t.Fatalf("Delete after a cancel past the commit: %v", err)
	}
	if e, _ := s.Entry(K8sWriteNamespaces); e.Source == SourceSet {
		t.Fatalf("snapshot after Delete = %+v, want the override gone", e)
	}
}
