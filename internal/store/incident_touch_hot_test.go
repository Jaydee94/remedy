package store

import (
	"context"
	"strings"
	"testing"
)

// The poller touches every open signal incident once a minute: last_seen, host_id and details are rewritten (about 25,000
// versions a day on the home server). An update that changes no indexed column is a HOT update; PostgreSQL then clears the old
// versions of a page by itself when the page is read, with no vacuum. last_seen used to be in the index incidents_state_seen,
// which made every touch a full update with an entry in every index. Where autovacuum could not start (AppArmor on kernel 7.0
// denies the signals between the processes of a container), 41 live rows filled 2089 pages (16 MB) and their index 378 pages.
// Measured on a scratch database with rows of 2 KB, 19 rows and 6000 touches, autovacuum off: 37 pages with the index, 4 without.
// The page count itself depends on what else runs on the database (an old transaction anywhere holds back the cleaning), so this
// test checks the cause: no index of incidents may contain a column that a touch writes.
func TestNoIndexOfIncidentsContainsAColumnThatEveryTouchWrites(t *testing.T) {
	_, db := schemaTest(t)
	rows, err := db.QueryContext(context.Background(), `SELECT indexname, indexdef FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'incidents'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			t.Fatal(err)
		}
		seen++
		for _, col := range []string{"last_seen", "details"} {
			if strings.Contains(def, col) {
				t.Errorf("index %s contains %s, which TouchIncidentSignal rewrites at every poll: the touch is no HOT update (%s)", name, col, def)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Fatal("no index of incidents found: the query looks at the wrong schema")
	}
}
