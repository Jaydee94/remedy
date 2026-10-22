-- role: who the run is for. incident_id: the incident a responder run diagnoses; the link is cleared,
-- not cascaded, when the incident goes away, so the run stays readable. output: the structured
-- result of the run as JSON. failure_reason: why a failed run failed when it was not the agent's
-- own exit code (timeout).
ALTER TABLE runs ADD COLUMN role TEXT NOT NULL DEFAULT 'adhoc' CHECK (role IN ('adhoc', 'responder'));
ALTER TABLE runs ADD COLUMN incident_id INTEGER REFERENCES incidents (id) ON DELETE SET NULL;
ALTER TABLE runs ADD COLUMN output TEXT;
ALTER TABLE runs ADD COLUMN failure_reason TEXT NOT NULL DEFAULT '';

CREATE INDEX runs_incident ON runs (incident_id);
