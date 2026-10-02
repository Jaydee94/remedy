package server

import (
	"context"

	"github.com/Jaydee94/remedy/internal/store"
)

// logActivity appends to the activity log. It is best effort: a handler that already did its work
// must not fail because the log entry could not be written.
func (s *srv) logActivity(ctx context.Context, kind string, repoID int64, summary string) {
	_ = s.d.Store.AddActivity(ctx, store.NewActivity{Kind: kind, RepoID: repoID, Summary: summary})
}
