-- diagnosed_sha: the commit the stored diagnosis is about. A new head commit that fails again makes the
-- incident eligible for a new automatic diagnosis.
ALTER TABLE incidents ADD COLUMN diagnosed_sha TEXT NOT NULL DEFAULT '';

-- head_sha: the commit the prompt and the snapshot of a responder run are for. automatic: the run was
-- started by Remedy, not by a click; the daily limit counts these.
ALTER TABLE runs ADD COLUMN head_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN automatic INTEGER NOT NULL DEFAULT 0;

CREATE INDEX runs_responder_created ON runs (role, automatic, created_at);
