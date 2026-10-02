CREATE TABLE runs (
  id          TEXT PRIMARY KEY,
  provider    TEXT NOT NULL,
  prompt      TEXT NOT NULL,
  status      TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
  exit_code   INTEGER,
  result      TEXT NOT NULL DEFAULT '',
  session_id  TEXT NOT NULL DEFAULT '',
  cost_usd    REAL NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL,
  started_at  TEXT,
  finished_at TEXT
);

CREATE INDEX runs_status_created ON runs (status, created_at);

CREATE TABLE run_events (
  run_id     TEXT NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
  seq        INTEGER NOT NULL,
  kind       TEXT NOT NULL,
  payload    TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (run_id, seq)
);
