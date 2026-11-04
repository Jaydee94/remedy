-- The poller touches every open signal incident once a minute: last_seen, host_id and details are rewritten, 25,000 times a day
-- on the home server. last_seen was in the index incidents_state_seen (state, last_seen), so every touch changed an indexed
-- column: a full update that adds an entry to every index and leaves a dead row version behind that only a vacuum removes.
-- Where autovacuum cannot start (AppArmor on kernel 7.0 denies the signals between the processes of a container) 41 live rows
-- filled 2089 pages (16 MB) and the index 378 pages. Without last_seen in an index a touch is a HOT update, and PostgreSQL
-- clears the old versions of a page by itself when the page is read. Nothing needs the index: the table holds a few dozen rows,
-- and incidents_source_state (source, state) stays for the filters by source.
DROP INDEX incidents_state_seen;
