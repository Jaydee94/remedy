-- Skills: short Markdown notes about this lab, global (host_id NULL) or bound to one host. A proposal (state 'proposed') is invisible to
-- every run until the maintainer confirms it; 'off' keeps a skill but hides it. Deleting a host deletes its skills and proposals with it
-- (ON DELETE CASCADE). run_id is the learning run that proposed it; evidence is JSON, [{"argv": [...], "ran": true}, ...]; a proposal that
-- replaces a skill has the same name in the same scope, so the unique index leaves proposals out and the confirmation (one transaction)
-- updates the existing row. The table keeps no secret. Timestamps have nine fractional digits.
CREATE TABLE skills (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  host_id      INTEGER REFERENCES hosts (id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  description  TEXT NOT NULL,
  body         TEXT NOT NULL,
  origin       TEXT NOT NULL CHECK (origin IN ('written', 'proposed')),
  state        TEXT NOT NULL CHECK (state IN ('proposed', 'active', 'off')),
  run_id       TEXT REFERENCES runs (id) ON DELETE SET NULL,
  evidence     TEXT NOT NULL DEFAULT '[]',
  replaces_id  INTEGER REFERENCES skills (id) ON DELETE SET NULL,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  confirmed_at TEXT
);
CREATE UNIQUE INDEX skills_scope_name ON skills (COALESCE(host_id, 0), name) WHERE state <> 'proposed';
CREATE INDEX skills_host_state ON skills (host_id, state);
