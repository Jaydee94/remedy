-- remedy:foreign-keys-off
-- The role learner. runs.role has a CHECK (migration 004), and SQLite cannot change a CHECK in place, so runs is rebuilt with
-- 'learner' allowed. Every column and every index of runs is carried over as the migrations 001, 004, 005, 006, 007 and 011 left
-- them. The first line makes the migration runner switch foreign keys off for this file: dropping a table that other tables point
-- at, with foreign keys on, runs their ON DELETE actions, which would delete every event, token, tool call and host binding of
-- every run and clear the links of incidents, activity, incident_notes and skills (008 explains the same for incidents). The runner
-- checks the foreign keys before it commits. runs has a text key, so there is no sequence to carry over.
CREATE TABLE runs_new (
  id                TEXT PRIMARY KEY,
  provider          TEXT NOT NULL,
  prompt            TEXT NOT NULL,
  status            TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
  exit_code         INTEGER,
  result            TEXT NOT NULL DEFAULT '',
  session_id        TEXT NOT NULL DEFAULT '',
  cost_usd          REAL NOT NULL DEFAULT 0,
  created_at        TEXT NOT NULL,
  started_at        TEXT,
  finished_at       TEXT,
  role              TEXT NOT NULL DEFAULT 'adhoc' CHECK (role IN ('adhoc', 'responder', 'learner')),
  incident_id       INTEGER REFERENCES incidents (id) ON DELETE SET NULL,
  output            TEXT,
  failure_reason    TEXT NOT NULL DEFAULT '',
  head_sha          TEXT NOT NULL DEFAULT '',
  automatic         INTEGER NOT NULL DEFAULT 0,
  mcp               INTEGER NOT NULL DEFAULT 0,
  cancel_requested  INTEGER NOT NULL DEFAULT 0,
  last_heartbeat_at TEXT,
  cluster           INTEGER NOT NULL DEFAULT 0,
  host_count        INTEGER NOT NULL DEFAULT 0
);

INSERT INTO runs_new (id, provider, prompt, status, exit_code, result, session_id, cost_usd, created_at, started_at, finished_at,
    role, incident_id, output, failure_reason, head_sha, automatic, mcp, cancel_requested, last_heartbeat_at, cluster, host_count)
  SELECT id, provider, prompt, status, exit_code, result, session_id, cost_usd, created_at, started_at, finished_at,
    role, incident_id, output, failure_reason, head_sha, automatic, mcp, cancel_requested, last_heartbeat_at, cluster, host_count
  FROM runs;

DROP TABLE runs;
ALTER TABLE runs_new RENAME TO runs;

CREATE INDEX runs_status_created ON runs (status, created_at);
CREATE INDEX runs_incident ON runs (incident_id);
CREATE INDEX runs_responder_created ON runs (role, automatic, created_at);
