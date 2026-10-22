package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/Jaydee94/remedy/internal/secret"
)

// db is what a query of the store runs on: the pool or a transaction. Both rewrite the store's ? placeholders to
// PostgreSQL's $1, $2, ... before the statement reaches the driver.
//
// Statement texts are expected to be constant: a string constant, or text assembled only from constants and from a
// choice among fixed fragments (an optional WHERE condition, an IN list of n placeholders). Every value travels as an
// argument; dynamic text must never embed one (not a time, an id or a name). The rewrite of each text is cached
// (bounded, see maxRebound), and pgx prepares each distinct text on the server, so a text that changes with its values
// costs a prepare per call and evicts the prepared statements that are reused.
//
// The rewrite knows single-quoted literals (a doubled quote inside them too), double-quoted identifiers, dollar-quoted strings ($$ and
// $tag$), -- comments and /* */ comments. Not supported, and not to be used in the store's SQL: E'...' strings with
// backslash-escaped quotes, the JSONB operators ?, ?| and ?& (they would be taken for placeholders), $ inside an
// identifier, nested block comments, and mixing $1-style parameters with ? in one statement.
//
// Both also clean the text arguments they are given (cleanArgs): a NUL byte or an invalid UTF-8 sequence becomes U+FFFD,
// because PostgreSQL refuses either in a text value (SQLSTATE 22021) and the store writes untrusted text (pod logs, host
// output, an agent's answer, result and notes) that may hold them. This is a deliberate change from SQLite, which stored such
// bytes as they were: a write that would fail now succeeds with the replacement character in place of the bad bytes.
// What is covered is a string, a *string, a valid sql.NullString and a value of a named type whose kind is string. Bytes
// ([]byte, which is how sealed values and hashes reach their BYTEA columns) are sent as they are. Arrays and every other
// slice type are NOT cleaned, for example the []string of an "= ANY(?)" (incidents.go, flushActivity): a caller that passes
// a slice of text must clean its elements itself.
type db interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

var (
	_ db = (*pool)(nil)
	_ db = (*txn)(nil)
)

// pool wraps the connection pool. It offers only the context methods that rewrite placeholders, so a statement cannot
// reach the driver with ? in it by way of Exec or Query.
type pool struct{ db *sql.DB }

func (p *pool) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	q, err := rebindErr(query)
	if err != nil {
		return nil, err
	}
	return p.db.ExecContext(ctx, q, cleanArgs(args)...)
}

func (p *pool) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	q, err := rebindErr(query)
	if err != nil {
		return nil, err
	}
	return p.db.QueryContext(ctx, q, cleanArgs(args)...)
}

// QueryRowContext panics on a statement the rewrite refuses: *sql.Row cannot carry an error of its own. No such statement
// ships (TestNoStoreSQLHasAQuotedQuestionMark scans the sources).
func (p *pool) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return p.db.QueryRowContext(ctx, rebind(query), cleanArgs(args)...)
}

func (p *pool) Close() error { return p.db.Close() }

// txn wraps one transaction, with the same rewrite as pool. acts holds the activity entries the transaction logs: insertActivity
// queues them and runTx writes them all just before the commit (flushActivity), so that ids are drawn in commit order.
type txn struct {
	tx   *sql.Tx
	acts []NewActivity
}

func (t *txn) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	q, err := rebindErr(query)
	if err != nil {
		return nil, err
	}
	return t.tx.ExecContext(ctx, q, cleanArgs(args)...)
}

func (t *txn) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	q, err := rebindErr(query)
	if err != nil {
		return nil, err
	}
	return t.tx.QueryContext(ctx, q, cleanArgs(args)...)
}

// QueryRowContext panics on a refused statement, like pool.QueryRowContext.
func (t *txn) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, rebind(query), cleanArgs(args)...)
}

// rebound caches the rewrite of each statement text. The store's texts are constant (see db), so the set of distinct
// texts is small and fixed: the queries, plus one per length of a dynamic IN list. The cache is capped at maxRebound
// entries anyway, so that a text that does change with its values cannot grow it without bound: once full, new texts
// are rewritten on every call (a linear scan) and not stored.
var (
	rebound  sync.Map // string -> rebindResult
	reboundN atomic.Int64
)

// maxRebound is the most statement texts rebound holds.
const maxRebound = 1024

type rebindResult struct {
	q   string
	err error
}

// reboundLen counts the entries of rebound (tests).
func reboundLen() int {
	n := 0
	rebound.Range(func(_, _ any) bool { n++; return true })
	return n
}

// rebind rewrites the ? placeholders of query to $1, $2, ... and panics on a statement rebindErr refuses.
func rebind(query string) string {
	q, err := rebindErr(query)
	if err != nil {
		panic(err)
	}
	return q
}

// rebindErr rewrites the ? placeholders of query to $1, $2, ... in order. It refuses a question mark inside a quoted
// literal, a quoted identifier, a dollar-quoted string or a comment, and an unterminated quote or comment: such a mark is
// either not a placeholder or a mistake, and guessing would shift the numbering of every later one.
func rebindErr(query string) (string, error) {
	if v, ok := rebound.Load(query); ok {
		r := v.(rebindResult)
		return r.q, r.err
	}
	q, err := rewrite(query)
	if reboundN.Add(1) > maxRebound {
		reboundN.Add(-1) // full: do not store
	} else if _, loaded := rebound.LoadOrStore(query, rebindResult{q, err}); loaded {
		reboundN.Add(-1) // another caller stored it first
	}
	return q, err
}

var errQuotedMark = errors.New("store: a statement has a question mark inside quotes or a comment; the placeholder rewrite refuses it")

func rewrite(query string) (string, error) {
	if !strings.Contains(query, "?") {
		return query, nil
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	for i := 0; i < len(query); {
		c := query[i]
		switch {
		case c == '?':
			n++
			fmt.Fprintf(&b, "$%d", n)
			i++
		case c == '\'' || c == '"':
			// '' and "" inside a literal are two adjacent quoted parts; scanning them one after the other is the same.
			end := strings.IndexByte(query[i+1:], c)
			if end < 0 {
				return "", errQuotedMark
			}
			part := query[i : i+1+end+1]
			if strings.Contains(part, "?") {
				return "", errQuotedMark
			}
			b.WriteString(part)
			i += len(part)
		case c == '-' && strings.HasPrefix(query[i:], "--"):
			end := strings.IndexByte(query[i:], '\n')
			part := query[i:]
			if end >= 0 {
				part = query[i : i+end]
			}
			if strings.Contains(part, "?") {
				return "", errQuotedMark
			}
			b.WriteString(part)
			i += len(part)
		case c == '/' && strings.HasPrefix(query[i:], "/*"):
			end := strings.Index(query[i+2:], "*/")
			if end < 0 {
				return "", errQuotedMark
			}
			part := query[i : i+2+end+2]
			if strings.Contains(part, "?") {
				return "", errQuotedMark
			}
			b.WriteString(part)
			i += len(part)
		case c == '$':
			tag, ok := dollarTag(query[i:])
			if !ok {
				b.WriteByte(c)
				i++
				continue
			}
			end := strings.Index(query[i+len(tag):], tag)
			if end < 0 {
				return "", errQuotedMark
			}
			part := query[i : i+len(tag)+end+len(tag)]
			if strings.Contains(part, "?") {
				return "", errQuotedMark
			}
			b.WriteString(part)
			i += len(part)
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), nil
}

// dollarTag reports the opening tag of a dollar-quoted string at the start of s: $$ or $name$ (a name does not start with
// a digit, so $1 is not a tag).
func dollarTag(s string) (string, bool) {
	for j := 1; j < len(s); j++ {
		c := s[j]
		switch {
		case c == '$':
			return s[:j+1], true
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9' && j > 1:
		default:
			return "", false
		}
	}
	return "", false
}

// pgCode returns the SQLSTATE of a PostgreSQL error in err's chain, or "".
func pgCode(err error) (string, *pgconn.PgError) {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code, pe
	}
	return "", nil
}

// isUnique reports whether err is a unique violation (SQLSTATE 23505).
func isUnique(err error) bool {
	code, _ := pgCode(err)
	return code == "23505"
}

// uniqueConstraint returns the name of the constraint (or unique index) a unique violation broke, or "".
func uniqueConstraint(err error) string {
	code, pe := pgCode(err)
	if code != "23505" {
		return ""
	}
	return pe.ConstraintName
}

// isSerialization reports whether err is a serialization failure (SQLSTATE 40001): the transaction may be retried.
func isSerialization(err error) bool {
	code, _ := pgCode(err)
	return code == "40001"
}

// isLockTimeout reports whether err is a lock wait that ran past lock_timeout (SQLSTATE 55P03).
func isLockTimeout(err error) bool {
	code, _ := pgCode(err)
	return code == "55P03"
}

// maxSerializationRetries bounds how often inTxIso runs a transaction again after a serialization failure.
const maxSerializationRetries = 5

// deleteLockTimeout bounds each lock wait of a delete that cascades (inDeleteTx). It is far below PostgreSQL's default
// deadlock_timeout of one second, so a delete gives up its locks before another transaction's deadlock check could pick that
// transaction as the victim. maxDeleteAttempts bounds the attempts.
const (
	deleteLockTimeout = "50ms"
	maxDeleteAttempts = 40
)

// ErrDatabaseBusy is in the chain of the error of a delete whose lock waits kept running out (inDeleteTx): other transactions held
// the rows it needs for all of its attempts. Nothing was deleted; the same call may succeed a moment later.
var ErrDatabaseBusy = errors.New("store: the database is busy; try again")

// busyError is ErrDatabaseBusy with the last lock timeout as its cause: its text is ErrDatabaseBusy's alone (the raw "canceling statement
// due to lock timeout" means nothing to a person), and errors.Is and errors.As reach both.
type busyError struct{ cause error }

func (e *busyError) Error() string   { return ErrDatabaseBusy.Error() }
func (e *busyError) Unwrap() []error { return []error{ErrDatabaseBusy, e.cause} }

// inDeleteTx runs fn, a delete whose foreign key actions cascade, in a transaction whose lock waits are bounded by
// deleteLockTimeout, and runs it again after a pause when a wait runs out. fn must be idempotent, like an inTxIso closure.
// When the attempts run out it returns an error with ErrDatabaseBusy (and the last lock timeout) in its chain.
//
// Why: such a delete locks the parent row first and the children after it (the cascade), while every other transaction locks a
// child first (an incident, a run, a proposal) and the parent after it (the FOR KEY SHARE of its foreign key check, or of
// flushActivity). The two orders can close a wait cycle, which PostgreSQL ends by aborting one of the two with a deadlock error
// (40P01) after deadlock_timeout. A delete that never waits long is in practice not part of such a cycle: it steps back before
// the deadlock check runs (as long as deadlock_timeout stays well above deleteLockTimeout; Open warns when it does not), the
// other transaction finishes, and the delete tries again.
func (s *Store) inDeleteTx(ctx context.Context, fn func(tx *txn) error) error {
	var err error
	for attempt := 0; attempt < maxDeleteAttempts; attempt++ {
		if attempt > 0 {
			t := time.NewTimer(time.Duration(min(attempt, 10))*5*time.Millisecond + rand.N(10*time.Millisecond))
			select {
			case <-ctx.Done():
				t.Stop()
				return fmt.Errorf("%w (after a lock wait of a delete ran out: %w)", ctx.Err(), err)
			case <-t.C:
			}
		}
		err = s.runTx(ctx, nil, func(tx *txn) error {
			if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout = '`+deleteLockTimeout+`'`); err != nil {
				return err
			}
			return fn(tx)
		})
		if !isLockTimeout(err) {
			return err
		}
	}
	return &busyError{cause: err}
}

// inTx runs fn in a transaction at the server's default isolation (READ COMMITTED). fn must use only tx: a query on s.db
// runs on another connection of the pool, outside the transaction.
func (s *Store) inTx(ctx context.Context, fn func(tx *txn) error) error {
	return s.runTx(ctx, nil, fn)
}

// inTxIso runs fn in a transaction at level and runs it again, up to maxSerializationRetries times, when it fails with a
// serialization failure (in fn or at the commit). fn must therefore be idempotent: no effect outside tx, and it must reset
// any Go state it captures at its start (a closure that appends to a slice of the caller would pile up rows across the
// attempts). A context that ends during the pause between two attempts ends the loop with ctx.Err(), wrapped together
// with the last serialization failure.
func (s *Store) inTxIso(ctx context.Context, level sql.IsolationLevel, fn func(tx *txn) error) error {
	opts := &sql.TxOptions{Isolation: level}
	var err error
	for attempt := 0; attempt <= maxSerializationRetries; attempt++ {
		if attempt > 0 {
			if ctx.Err() != nil {
				return fmt.Errorf("%w (after a serialization failure: %w)", ctx.Err(), err)
			}
			t := time.NewTimer(time.Duration(attempt)*5*time.Millisecond + rand.N(5*time.Millisecond))
			select {
			case <-ctx.Done():
				t.Stop()
				return fmt.Errorf("%w (after a serialization failure: %w)", ctx.Err(), err)
			case <-t.C:
			}
		}
		err = s.runTx(ctx, opts, fn)
		if !isSerialization(err) {
			return err
		}
	}
	return err
}

// runTx runs fn in one transaction: commit on nil, roll back on an error, and roll back on any other way out of fn, a panic
// (which goes on) or runtime.Goexit (t.Fatal inside fn), so the connection goes back to the pool clean. The activity
// entries fn queued are written between fn and the commit.
func (s *Store) runTx(ctx context.Context, opts *sql.TxOptions, fn func(tx *txn) error) error {
	tx, err := s.db.db.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback() // after the commit was tried, Rollback is a no-op that returns ErrTxDone
		}
	}()
	t := &txn{tx: tx}
	if err := fn(t); err != nil {
		return err
	}
	if err := t.flushActivity(ctx); err != nil {
		return err
	}
	committed = true
	return tx.Commit()
}

// DefaultMaxConns is the size of the pool when no WithMaxConns is given.
const DefaultMaxConns = 10

// Option changes how Open connects.
type Option func(*options)

type options struct {
	maxConns int
	// connectWait bounds how long Open waits for a server that cannot be reached yet; backoffMin and backoffMax bound the
	// pause between two attempts (it doubles from min up to max).
	connectWait, backoffMin, backoffMax time.Duration
	log                                 *slog.Logger
}

func defaultOptions() options {
	return options{maxConns: DefaultMaxConns, connectWait: 30 * time.Second, backoffMin: time.Second, backoffMax: 5 * time.Second}
}

// WithMaxConns sets the largest number of open connections of the pool.
func WithMaxConns(n int) Option { return func(o *options) { o.maxConns = n } }

// WithLogger sets where Open logs its warnings about the server's settings; slog.Default() when not given.
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.log = l } }

// withConnectWait shortens the wait for an unreachable server (tests).
func withConnectWait(total, minPause, maxPause time.Duration) Option {
	return func(o *options) { o.connectWait, o.backoffMin, o.backoffMax = total, minPause, maxPause }
}

// errURL is all Open says about a URL it cannot use: the URL holds a password, and the parser's own errors quote it.
var errURL = errors.New("store: the database URL cannot be parsed (it is not shown: it holds a password)")

// Open connects to the PostgreSQL database at url and migrates it. A server that cannot be reached yet (a database that is
// still starting) is tried again for a bounded time; a server that answers with an error (a refused login, a missing
// database) is not. No error of Open contains the URL or its password.
func Open(ctx context.Context, url secret.Value, opts ...Option) (*Store, error) {
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}
	if o.maxConns < 1 {
		return nil, fmt.Errorf("store: the pool needs at least one connection, not %d", o.maxConns)
	}
	raw := url.Reveal()
	cfg, err := pgx.ParseConfig(raw)
	if err != nil {
		return nil, errURL
	}
	sqlDB := sql.OpenDB(stdlib.GetConnector(*cfg))
	sqlDB.SetMaxOpenConns(o.maxConns)
	sqlDB.SetMaxIdleConns(o.maxConns)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	if err := ping(ctx, sqlDB, o); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("store: cannot reach the database: %w", scrub(err, raw, cfg.Password))
	}
	s := &Store{db: &pool{db: sqlDB}}
	if err := s.migrate(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", scrub(err, raw, cfg.Password))
	}
	log := o.log
	if log == nil {
		log = slog.Default()
	}
	s.checkDeadlockTimeout(ctx, log)
	return s, nil
}

// minDeadlockTimeoutMS is the smallest deadlock_timeout Open accepts without a warning: four times deleteLockTimeout, so a
// cascading delete steps back well before the deadlock check could pick another transaction as the victim (inDeleteTx).
const minDeadlockTimeoutMS = 200

func deadlockTimeoutTooLow(ms int) bool { return ms < minDeadlockTimeoutMS }

// checkDeadlockTimeout logs one warning when the server's deadlock_timeout is below minDeadlockTimeoutMS. It changes
// nothing: the store works either way, only the rare wait cycle of a delete may then end in a deadlock error instead of a
// retry. pg_settings gives the value in milliseconds.
func (s *Store) checkDeadlockTimeout(ctx context.Context, log *slog.Logger) {
	var ms int
	if err := s.db.QueryRowContext(ctx, `SELECT setting::int FROM pg_settings WHERE name = 'deadlock_timeout'`).Scan(&ms); err != nil {
		log.Warn("cannot read the database's deadlock_timeout", "err", err)
		return
	}
	if deadlockTimeoutTooLow(ms) {
		log.Warn("the database's deadlock_timeout is low: a delete that waits for a lock may end in a deadlock error instead of trying again",
			"deadlockTimeoutMS", ms, "recommendedMinimumMS", minDeadlockTimeoutMS)
	}
}

// ping waits for the server: it tries until o.connectWait has passed, pausing between attempts from o.backoffMin, doubling,
// up to o.backoffMax. An error the server itself sent ends the wait at once, except "cannot connect now" (57P03, a server
// that is starting or shutting down).
func ping(ctx context.Context, d *sql.DB, o options) error {
	deadline := time.Now().Add(o.connectWait)
	pause := o.backoffMin
	for {
		attemptCtx, cancel := context.WithDeadline(ctx, deadline)
		err := d.PingContext(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		if !retryable(err) || ctx.Err() != nil {
			return err
		}
		left := time.Until(deadline)
		if left <= 0 {
			return err
		}
		wait := min(pause, left)
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return err
		case <-t.C:
		}
		pause = min(pause*2, o.backoffMax)
	}
}

// retryable reports whether a failed connection attempt may succeed later: the server was not reached, or it said it
// cannot accept connections yet. Any failure without an answer of the server (refused, timed out, name not resolved) is
// taken to be of the first kind.
func retryable(err error) bool {
	if code, _ := pgCode(err); code != "" {
		return code == "57P03"
	}
	return true
}

// scrubbedError carries an error whose text held a secret: Error() is the text with the secret replaced, Unwrap keeps the
// chain (so a caller can still find the *pgconn.PgError with errors.As). Only Error() is ever printed: the type has no
// Format method, so fmt's verbs use Error() too.
type scrubbedError struct {
	err error
	msg string
}

func (e *scrubbedError) Error() string { return e.msg }
func (e *scrubbedError) Unwrap() error { return e.err }

// scrub returns err with every non-empty secret replaced by *** in its text, the chain kept.
func scrub(err error, secrets ...string) error {
	msg := err.Error()
	for _, sec := range secrets {
		if sec != "" {
			msg = strings.ReplaceAll(msg, sec, "***")
		}
	}
	return &scrubbedError{err: err, msg: msg}
}
