-- The deadline of an approval in an automatic run: set when the call starts waiting (request time plus auto_approval_timeout), empty for a
-- read call, for a run that a click started and when the expiry is off. An undecided call whose deadline has passed ends as abandoned.
ALTER TABLE tool_calls ADD COLUMN expires_at TEXT;
