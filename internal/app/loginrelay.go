package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/Jaydee94/remedy/internal/loginrelay"
	"github.com/Jaydee94/remedy/internal/store"
)

// loginEntries are the activity kind and the fixed summary of each event of a login session.
var loginEntries = map[loginrelay.EventKind]struct{ kind, summary string }{
	loginrelay.EventStarted:   {store.KindLoginStarted, "The runner's login was started from the Setup page."},
	loginrelay.EventSucceeded: {store.KindLoginSucceeded, "The runner's login from the Setup page worked."},
	loginrelay.EventFailed:    {store.KindLoginFailed, "The runner's login from the Setup page did not work."},
	loginrelay.EventCancelled: {store.KindLoginCancelled, "The runner's login from the Setup page was cancelled."},
}

// loginActivity is the manager's event hook: one activity entry per event, with a fixed summary and data that is {} or
// the reason word. It links to nothing (a login is not a run) and holds no address, code or session id. It is one short
// store write and never calls the manager.
func loginActivity(st *store.Store, log *slog.Logger) func(loginrelay.Event) {
	return func(ev loginrelay.Event) {
		entry, ok := loginEntries[ev.Kind]
		if !ok {
			return
		}
		var data json.RawMessage
		if ev.Reason.Known() {
			data, _ = json.Marshal(map[string]string{"reason": string(ev.Reason)}) // a word of a closed set
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := st.AddActivity(ctx, store.NewActivity{Kind: entry.kind, Summary: entry.summary, Data: data}); err != nil {
			log.Warn("could not write the activity of the login", "kind", entry.kind, "err", err)
		}
	}
}

// loginRelay builds the login relay's manager: a login is refused while a run is running, and each state it reaches is
// written to the activity log.
func loginRelay(st *store.Store, log *slog.Logger) *loginrelay.Manager {
	return loginrelay.New(loginrelay.Config{
		RunActive: func(ctx context.Context) (bool, error) {
			running, _, err := st.RunCounts(ctx)
			return running > 0, err
		},
		OnEvent: loginActivity(st, log),
		Log:     log,
	})
}
