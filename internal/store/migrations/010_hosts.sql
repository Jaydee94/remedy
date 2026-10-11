-- Hosts: machines outside the cluster that Remedy may be given a login to. A credential belongs to one host and is
-- stored sealed (AES-256-GCM, bound to the host id); the host key the machine must present is pinned in the host's row.
CREATE TABLE hosts (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  name           TEXT NOT NULL UNIQUE COLLATE NOCASE,
  address        TEXT NOT NULL,
  port           INTEGER NOT NULL DEFAULT 22 CHECK (port BETWEEN 1 AND 65535),
  account        TEXT NOT NULL DEFAULT 'remedy',
  notes          TEXT NOT NULL DEFAULT '',
  docker_pattern TEXT NOT NULL DEFAULT '',
  systemd_units  TEXT NOT NULL DEFAULT '[]',
  enabled        INTEGER NOT NULL DEFAULT 1,
  pinned_key     TEXT NOT NULL DEFAULT '',
  pinned_at      TEXT,
  status         TEXT NOT NULL DEFAULT 'new'
                 CHECK (status IN ('new', 'untrusted', 'ok', 'error', 'key_changed', 'undecryptable')),
  status_detail  TEXT NOT NULL DEFAULT '',
  report         TEXT NOT NULL DEFAULT '',
  checked_at     TEXT,
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL
);

CREATE TABLE host_credentials (
  host_id    INTEGER PRIMARY KEY REFERENCES hosts (id) ON DELETE CASCADE,
  kind       TEXT NOT NULL CHECK (kind IN ('key', 'password')),
  ciphertext BLOB NOT NULL,
  hint       TEXT NOT NULL,
  public_key TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);
