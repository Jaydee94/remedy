-- A resolved alert that fires again comes back as the same incident (see the spec of alert recurrence). An incident the
-- maintainer ignored must stay ignored when it resolves by absence and returns, so ResolveIncident records that it was
-- ignored at that moment. Rows resolved before this migration are 0. A plain column: no table is rebuilt.
ALTER TABLE incidents ADD COLUMN resolved_while_ignored INTEGER NOT NULL DEFAULT 0;
