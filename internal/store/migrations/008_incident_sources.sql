-- remedy:foreign-keys-off
-- Incidents of more than one source. incidents is rebuilt because repo_id becomes optional, and SQLite cannot drop NOT NULL
-- in place. The first line makes the migration runner switch foreign keys off for this file: dropping a table that
-- other tables point at, with foreign keys on, runs their ON DELETE actions, which would delete every note
-- (incident_notes cascades) and clear the links of activity, runs and tool_calls. The runner checks the foreign keys
-- before it commits.
--
-- source: github, alertmanager or argocd. key: the identity inside the source, computed by the source; for GitHub it is
-- repo_id, ref and check_name joined with the character U+001F, which no ref can contain (the code does the same in
-- store.GitHubKey). title: a short line for the list. severity: critical, warning, info or none. auto_diagnose: set when
-- the incident opens, or for GitHub when a new conclusion arrives; it says whether the responder may start on its own.
-- details: the signal as bounded JSON. repo_id is set for GitHub incidents and only for them.
CREATE TABLE incidents_new (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  source            TEXT NOT NULL DEFAULT 'github' CHECK (source IN ('github', 'alertmanager', 'argocd')),
  key               TEXT NOT NULL,
  title             TEXT NOT NULL DEFAULT '',
  severity          TEXT NOT NULL DEFAULT 'none' CHECK (severity IN ('critical', 'warning', 'info', 'none')),
  auto_diagnose     INTEGER NOT NULL DEFAULT 0,
  details           TEXT NOT NULL DEFAULT '{}',
  repo_id           INTEGER REFERENCES repos (id) ON DELETE CASCADE,
  ref               TEXT NOT NULL DEFAULT '',
  ref_url           TEXT NOT NULL DEFAULT '',
  check_name        TEXT NOT NULL DEFAULT '',
  state             TEXT NOT NULL CHECK (state IN ('open', 'diagnosing', 'diagnosed', 'resolved', 'ignored')),
  conclusion        TEXT NOT NULL,
  head_sha          TEXT NOT NULL DEFAULT '',
  check_url         TEXT NOT NULL DEFAULT '',
  occurrences       INTEGER NOT NULL DEFAULT 1,
  diagnoses         INTEGER NOT NULL DEFAULT 0,
  first_seen        TEXT NOT NULL,
  last_seen         TEXT NOT NULL,
  last_diagnosis_at TEXT,
  resolved_at       TEXT,
  resolved_reason   TEXT NOT NULL DEFAULT '',
  diagnosis         TEXT,
  run_id            TEXT REFERENCES runs (id) ON DELETE SET NULL,
  diagnosed_sha     TEXT NOT NULL DEFAULT '',
  CHECK ((source = 'github') = (repo_id IS NOT NULL))
);

INSERT INTO incidents_new (id, source, key, title, severity, auto_diagnose, details, repo_id, ref, ref_url, check_name, state,
    conclusion, head_sha, check_url, occurrences, diagnoses, first_seen, last_seen, last_diagnosis_at, resolved_at,
    resolved_reason, diagnosis, run_id, diagnosed_sha)
  SELECT id, 'github', repo_id || char(31) || ref || char(31) || check_name, check_name, 'none',
    CASE WHEN conclusion IN ('failure', 'timed_out', 'startup_failure') THEN 1 ELSE 0 END, '{}', repo_id, ref, ref_url, check_name, state,
    conclusion, head_sha, check_url, occurrences, diagnoses, first_seen, last_seen, last_diagnosis_at, resolved_at,
    resolved_reason, diagnosis, run_id, diagnosed_sha
  FROM incidents;

-- AUTOINCREMENT keeps the highest id it ever gave in sqlite_sequence. Carry it over, so that the id of a deleted incident
-- is not given again.
DELETE FROM sqlite_sequence WHERE name = 'incidents_new';
INSERT INTO sqlite_sequence (name, seq) SELECT 'incidents_new', seq FROM sqlite_sequence WHERE name = 'incidents';

DROP TABLE incidents;
ALTER TABLE incidents_new RENAME TO incidents;

CREATE UNIQUE INDEX incidents_active_key ON incidents (source, key) WHERE state <> 'resolved';
CREATE INDEX incidents_state_seen ON incidents (state, last_seen);
CREATE INDEX incidents_source_state ON incidents (source, state);
