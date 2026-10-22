-- mcp: the run has access to the gatekeeper (the MCP tools of the control plane). cancel_requested: the
-- maintainer cancelled the run; the runner learns it from its heartbeat. last_heartbeat_at: the last heartbeat
-- of the runner.
ALTER TABLE runs ADD COLUMN mcp INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN cancel_requested INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN last_heartbeat_at TEXT;

-- The token of a run with gatekeeper access. Only its SHA-256 is stored; revoked_at is set when the run ends.
CREATE TABLE run_tokens (
  run_id     TEXT PRIMARY KEY REFERENCES runs (id) ON DELETE CASCADE,
  token_hash BLOB NOT NULL UNIQUE,
  created_at TEXT NOT NULL,
  revoked_at TEXT
);

-- One row per tool call of an agent: the audit log, and for a mutating tool the approval. The rows are never
-- deleted. (run_id, tool_use_id) identifies a call, so that a repeat of it finds the first one.
-- status: running (a read tool, or an approved tool that executes), waiting (for a decision), succeeded, failed,
-- denied, abandoned (the agent went away, or the run ended, before the call was done).
-- decision: empty for a read tool; pending, approved, denied or abandoned for a mutating one.
CREATE TABLE tool_calls (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id          TEXT NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
  incident_id     INTEGER REFERENCES incidents (id) ON DELETE SET NULL,
  tool_use_id     TEXT NOT NULL,
  tool            TEXT NOT NULL,
  kind            TEXT NOT NULL CHECK (kind IN ('read', 'mutating')),
  arguments       TEXT NOT NULL DEFAULT '{}',
  status          TEXT NOT NULL CHECK (status IN ('running', 'waiting', 'succeeded', 'failed', 'denied', 'abandoned')),
  result          TEXT NOT NULL DEFAULT '',
  error           TEXT NOT NULL DEFAULT '',
  decision        TEXT NOT NULL DEFAULT '' CHECK (decision IN ('', 'pending', 'approved', 'denied', 'abandoned')),
  decision_reason TEXT NOT NULL DEFAULT '',
  decided_at      TEXT,
  created_at      TEXT NOT NULL,
  finished_at     TEXT,
  UNIQUE (run_id, tool_use_id)
);

CREATE INDEX tool_calls_decision ON tool_calls (decision, id);

-- Notes that an agent run added to an incident, with the maintainer's approval.
CREATE TABLE incident_notes (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  incident_id INTEGER NOT NULL REFERENCES incidents (id) ON DELETE CASCADE,
  run_id      TEXT REFERENCES runs (id) ON DELETE SET NULL,
  note        TEXT NOT NULL,
  created_at  TEXT NOT NULL
);

CREATE INDEX incident_notes_incident ON incident_notes (incident_id, id);
