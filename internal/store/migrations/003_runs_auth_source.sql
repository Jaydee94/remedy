-- How a run was billed, from the run's own init event (cycle 7b): "subscription" when the CLI said it used the login, "api_key" when
-- it said it used a key, empty when the event did not say (a run before this migration, or a CLI whose init has no such field).
-- The runner reports it with the outcome; the control plane sums cost_usd of the "api_key" runs for the daily budget. A column with a
-- default changes no row and rebuilds no table.
ALTER TABLE runs ADD COLUMN auth_source TEXT NOT NULL DEFAULT '' CHECK (auth_source IN ('', 'subscription', 'api_key'));
