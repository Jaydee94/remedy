package store

import "context"

// The store's transaction-level advisory locks (docs/research/postgres-concurrency-audit.md). They use the one-key bigint form of
// pg_advisory_xact_lock, which PostgreSQL keeps apart from the two-key form of the migration lock (migrateLockClass): the two never
// conflict. Each key is the bytes of "rmdy" followed by a number. A lock is held until its transaction commits or rolls back.
//
// Lock order: runStartLockKey, then skillsLockKey, each as the first statements of its transaction, before any row is locked;
// activityLockKey last, taken only by txn.flushActivity right before the commit. A transaction that holds a lock never waits for one
// that comes before it in this order.
const (
	advisoryKeyBase int64 = 0x726d_6479 << 32

	// activityLockKey orders the activity entries: ids are drawn under it and it is released after the commit, so id order is commit
	// order and a reader that follows the log by id misses nothing.
	activityLockKey = advisoryKeyBase | 1
	// runStartLockKey serialises the starts of a run that check "one run at a time" and the limits of automatic diagnosis:
	// StartDiagnosis and StartLearning.
	runStartLockKey = advisoryKeyBase | 2
	// skillsLockKey serialises the writes that check a limit of the skills: the most skills of a scope (CreateSkill, ConfirmSkill)
	// and the most proposals waiting for a host (CreateProposals, StartLearning).
	skillsLockKey = advisoryKeyBase | 3
)

// xactLock takes the advisory lock key for the rest of tx, waiting for a transaction that holds it.
func xactLock(ctx context.Context, tx *txn, key int64) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?)`, key)
	return err
}

// lockDiagnosis takes runStartLockKey: it must be the first statement of a transaction that starts a run after counting the runs
// that are queued or running, or the automatic ones of the day, or the diagnoses of an incident. Under it the counts hold until the
// commit.
func (s *Store) lockDiagnosis(ctx context.Context, tx *txn) error {
	return xactLock(ctx, tx, runStartLockKey)
}

// lockSkills takes skillsLockKey: it comes before any row lock of a transaction that counts the skills of a scope or the proposals
// of a host and then adds to them.
func (s *Store) lockSkills(ctx context.Context, tx *txn) error {
	return xactLock(ctx, tx, skillsLockKey)
}
