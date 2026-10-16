-- The rules that map an alert to a host, and the host of an incident. matchers is Alertmanager matcher syntax, one rule per
-- line, canonical (checked by the server, never executed); empty means the host is never named by an alert. host_id is set by
-- the Alertmanager source when an alert's labels match a host; deleting the host leaves the incident without one.
ALTER TABLE hosts ADD COLUMN matchers TEXT NOT NULL DEFAULT '';
ALTER TABLE incidents ADD COLUMN host_id INTEGER REFERENCES hosts (id) ON DELETE SET NULL;
CREATE INDEX incidents_host ON incidents (host_id);
