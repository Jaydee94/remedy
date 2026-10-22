package store

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/testutil/pgtest"
)

// A delete whose lock waits keep running out gives up with a sentence a person can read, and the cause stays reachable.
func TestADeleteThatStaysBlockedSaysTheDatabaseIsBusy(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	key := rand.Int64()

	// Another session holds an advisory lock; the delete's transaction waits for the same one, and lock_timeout ends each wait.
	holder := pgtest.Admin(t)
	conn, err := holder.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, key) }()

	attempts := 0
	err = s.inDeleteTx(ctx, func(tx *txn) error {
		attempts++
		_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?)`, key)
		return err
	})
	if err == nil {
		t.Fatal("the delete went through past a held lock")
	}
	if attempts != maxDeleteAttempts {
		t.Errorf("%d attempts, want %d", attempts, maxDeleteAttempts)
	}
	if !errors.Is(err, ErrDatabaseBusy) {
		t.Errorf("err = %v, want ErrDatabaseBusy", err)
	}
	if err.Error() != "store: the database is busy; try again" {
		t.Errorf("err text = %q", err.Error())
	}
	if !isLockTimeout(err) {
		t.Errorf("the lock timeout is not in the chain of %v", err)
	}
}

// A context that ends during the pause still reports the context, not the busy sentence.
func TestADeleteThatIsCancelledReportsTheContext(t *testing.T) {
	s := openTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	key := rand.Int64()
	holder := pgtest.Admin(t)
	conn, err := holder.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), `SELECT pg_advisory_lock($1)`, key); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, key) }()
	err = s.inDeleteTx(ctx, func(tx *txn) error {
		_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?)`, key)
		cancel()
		return err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestDeadlockTimeoutWarning(t *testing.T) {
	for ms, warn := range map[int]bool{1: true, 50: true, 199: true, 200: false, 1000: false, 60000: false} {
		if got := deadlockTimeoutTooLow(ms); got != warn {
			t.Errorf("deadlock_timeout %d ms: warn = %v, want %v", ms, got, warn)
		}
	}
}

// The server's default deadlock_timeout (1s) gives no warning, and reading it works on the test server.
func TestOpenWarnsNothingAtTheDefaultDeadlockTimeout(t *testing.T) {
	dsn := pgtest.New(t)
	var logs bytes.Buffer
	s, err := Open(context.Background(), secret.NewValue(dsn), WithMaxConns(2), WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if strings.Contains(logs.String(), "deadlock_timeout") {
		t.Fatalf("a warning at the default deadlock_timeout:\n%s", logs.String())
	}
	if strings.Contains(logs.String(), "level=ERROR") || strings.Contains(logs.String(), "cannot read") {
		t.Fatalf("reading deadlock_timeout failed:\n%s", logs.String())
	}
}
