-- Hosts of a run. A run gets its hosts when it is created and never changes them; the gatekeeper offers the host tools only
-- to a run that has some. host_count is the number of bindings at creation: a column and not a subquery, so that the
-- UPDATE ... RETURNING of ClaimNext and every other select of runs stay as they are. Deleting a host removes its binding
-- (ON DELETE CASCADE) but not the count; the host tools then find no usable host and say so.
ALTER TABLE runs ADD COLUMN host_count INTEGER NOT NULL DEFAULT 0;

CREATE TABLE run_hosts (
  run_id  TEXT    NOT NULL REFERENCES runs (id)  ON DELETE CASCADE,
  host_id INTEGER NOT NULL REFERENCES hosts (id) ON DELETE CASCADE,
  PRIMARY KEY (run_id, host_id)
);
CREATE INDEX run_hosts_host ON run_hosts (host_id);
