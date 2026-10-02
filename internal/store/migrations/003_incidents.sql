-- One incident per (repo, ref, check name) while it is not resolved. Ignored incidents keep their key
-- so that a new failure does not open a second one; they resolve like the others when the check is green.
-- diagnoses, last_diagnosis_at, diagnosis and run_id are for the responder (plan 1c).
CREATE TABLE incidents (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  repo_id           INTEGER NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
  ref               TEXT NOT NULL,
  ref_url           TEXT NOT NULL DEFAULT '',
  check_name        TEXT NOT NULL,
  state             TEXT NOT NULL CHECK (state IN ('open', 'diagnosing', 'diagnosed', 'resolved', 'ignored')),
  conclusion        TEXT NOT NULL,
  head_sha          TEXT NOT NULL,
  check_url         TEXT NOT NULL DEFAULT '',
  occurrences       INTEGER NOT NULL DEFAULT 1,
  diagnoses         INTEGER NOT NULL DEFAULT 0,
  first_seen        TEXT NOT NULL,
  last_seen         TEXT NOT NULL,
  last_diagnosis_at TEXT,
  resolved_at       TEXT,
  resolved_reason   TEXT NOT NULL DEFAULT '',
  diagnosis         TEXT,
  run_id            TEXT REFERENCES runs (id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX incidents_active_key ON incidents (repo_id, ref, check_name) WHERE state <> 'resolved';
CREATE INDEX incidents_state_seen ON incidents (state, last_seen);

-- Append-only. The links are nulled, not cascaded, when their target is deleted: the history stays and
-- its summary text still names what happened.
CREATE TABLE activity (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  at          TEXT NOT NULL,
  kind        TEXT NOT NULL,
  repo_id     INTEGER REFERENCES repos (id) ON DELETE SET NULL,
  incident_id INTEGER REFERENCES incidents (id) ON DELETE SET NULL,
  run_id      TEXT REFERENCES runs (id) ON DELETE SET NULL,
  summary     TEXT NOT NULL,
  data        TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX activity_incident ON activity (incident_id, id);
