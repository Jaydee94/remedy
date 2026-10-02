-- One connection for now (the code always uses id 1); the schema allows more without a migration.
CREATE TABLE github_connections (
  id               INTEGER PRIMARY KEY,
  token_ciphertext BLOB NOT NULL,
  token_hint       TEXT NOT NULL,
  login            TEXT NOT NULL,
  status           TEXT NOT NULL CHECK (status IN ('ok', 'error', 'undecryptable')),
  status_detail    TEXT NOT NULL DEFAULT '',
  checked_at       TEXT NOT NULL
);

CREATE TABLE repos (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  connection_id  INTEGER NOT NULL REFERENCES github_connections (id) ON DELETE CASCADE,
  full_name      TEXT NOT NULL UNIQUE COLLATE NOCASE,
  default_branch TEXT NOT NULL,
  enabled        INTEGER NOT NULL DEFAULT 1,
  last_polled_at TEXT,
  last_error     TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL
);
