package importsqlite

import (
	"context"
	"strings"
	"testing"
)

// dirtyText puts text PostgreSQL refuses into the fixture: a NUL in the one host's notes, invalid UTF-8 in every activity
// summary (three rows), and a NUL and an invalid byte in one run's result.
const dirtyText = `UPDATE hosts SET notes = 'a' || char(0) || 'b' WHERE id = 1;
	UPDATE activity SET summary = CAST(x'61ff62' AS TEXT);
	UPDATE runs SET result = 'x' || char(0) || CAST(x'fe' AS TEXT) || 'y' WHERE id = 'r-resp'`

// cleanedText is what dirtyText becomes with --replace-invalid-text, written as SQLite text: char(65533) is U+FFFD.
const cleanedText = `UPDATE hosts SET notes = 'a' || char(65533) || 'b' WHERE id = 1;
	UPDATE activity SET summary = 'a' || char(65533) || 'b';
	UPDATE runs SET result = 'x' || char(65533) || char(65533) || 'y' WHERE id = 'r-resp'`

func replacedCounts(rep Report) map[string]int {
	out := map[string]int{}
	for _, r := range rep.Replaced {
		out[r.Table+"."+r.Column] += r.Values
	}
	return out
}

func TestImportRefusesInvalidTextByDefault(t *testing.T) {
	src := withMigrations(t, buildSource(t), dirtyText)
	dst, raw := destination(t)
	_, err := Run(context.Background(), src, dst, Options{})
	if err == nil {
		t.Fatal("Run took text with a NUL and invalid UTF-8 without the option")
	}
	if !strings.Contains(err.Error(), "--replace-invalid-text") {
		t.Errorf("error %q does not name the option", err)
	}
	assertEmpty(t, raw)
}

func TestImportReplacesInvalidTextWhenToldTo(t *testing.T) {
	base := buildSource(t)
	src := withMigrations(t, base, dirtyText)
	want := withMigrations(t, base, cleanedText) // the source as it must arrive
	dst, raw := destination(t)

	rep, err := Run(context.Background(), src, dst, Options{ReplaceInvalidText: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := replacedCounts(rep)
	wantCounts := map[string]int{"hosts.notes": 1, "activity.summary": 3, "runs.result": 1}
	if len(got) != len(wantCounts) {
		t.Errorf("replaced = %v, want %v", got, wantCounts)
	}
	for k, n := range wantCounts {
		if got[k] != n {
			t.Errorf("replaced %s = %d, want %d (all: %v)", k, got[k], n, got)
		}
	}
	if rep.ReplacedTotal() != 5 {
		t.Errorf("ReplacedTotal = %d, want 5", rep.ReplacedTotal())
	}
	// The entries follow the copy order of the tables.
	pos := map[string]int{}
	for i, tr := range rep.Tables {
		pos[tr.Name] = i
	}
	for i := 1; i < len(rep.Replaced); i++ {
		if pos[rep.Replaced[i-1].Table] > pos[rep.Replaced[i].Table] {
			t.Errorf("replaced entries out of copy order: %+v", rep.Replaced)
		}
	}

	// Every table equals the cleaned source: the three columns hold U+FFFD, nothing else changed (sealed bytes included).
	wdb := openSourceForTest(t, want)
	for _, tb := range sqliteTables(t, wdb) {
		cols := sqliteColumns(t, wdb, tb)
		w := dump(t, wdb, tb, cols, orderOf[tb])
		g := dump(t, raw, tb, cols, orderOf[tb])
		if strings.Join(g, "\n") != strings.Join(w, "\n") {
			t.Errorf("table %s differs\n got: %v\nwant: %v", tb, g, w)
		}
	}
	// The secrets still open.
	h, err := dst.GetHost(context.Background(), 1)
	if err != nil || h.Credential == nil {
		t.Fatalf("GetHost: %+v, %v", h, err)
	}
	if plain, err := testKey(t).Open(h.Credential.Ciphertext, "host_credential:1"); err != nil || string(plain) != hostPlain {
		t.Fatalf("the host credential does not open: %v", err)
	}
}

func TestImportOfCleanTextReportsNoReplacement(t *testing.T) {
	dst, _ := destination(t)
	rep, err := Run(context.Background(), buildSource(t), dst, Options{ReplaceInvalidText: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Replaced) != 0 || rep.ReplacedTotal() != 0 {
		t.Fatalf("replaced = %+v for a clean file", rep.Replaced)
	}
}

func TestImportDryRunReportsTheReplacementsAndWritesNothing(t *testing.T) {
	src := withMigrations(t, buildSource(t), dirtyText)
	dst, raw := destination(t)
	rep, err := Run(context.Background(), src, dst, Options{DryRun: true, ReplaceInvalidText: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !rep.DryRun || rep.ReplacedTotal() != 5 || replacedCounts(rep)["activity.summary"] != 3 {
		t.Fatalf("dry run report = %+v", rep)
	}
	assertEmpty(t, raw)
}

func TestConvertValueReplacesOnlyTextAndOnlyWhenToldTo(t *testing.T) {
	if _, _, err := convertValue("a\x00", "text", false); err == nil {
		t.Error("a NUL was taken without replace")
	}
	if v, changed, err := convertValue("a\x00", "text", true); err != nil || !changed || v != "a�" {
		t.Errorf("NUL with replace = %q, %v, %v", v, changed, err)
	}
	if v, changed, err := convertValue([]byte("a\xffb"), "text", true); err != nil || !changed || v != "a�b" {
		t.Errorf("invalid UTF-8 blob in a text column with replace = %q, %v, %v", v, changed, err)
	}
	if v, changed, err := convertValue("Grüße", "text", true); err != nil || changed || v != "Grüße" {
		t.Errorf("clean text with replace = %q, %v, %v", v, changed, err)
	}
	raw := []byte{0, 0xff, 'x'}
	v, changed, err := convertValue(raw, "bytea", true)
	if err != nil || changed || string(v.([]byte)) != string(raw) {
		t.Errorf("bytea with replace = %x, %v, %v", v, changed, err)
	}
	if _, _, err := convertValue(int64(1), "text", true); err == nil {
		t.Error("replace made an integer acceptable in a text column")
	}
}

// Two keys that differ only in their bad bytes would become one key: the import refuses, it never merges two rows.
func TestImportRefusesTwoKeysThatTheReplacementWouldMerge(t *testing.T) {
	src := withMigrations(t, buildSource(t), `INSERT INTO settings (key, value, updated_at) VALUES
		('k' || char(0), '1', '2026-10-04T12:00:00.000000000Z'), ('k' || CAST(x'ff' AS TEXT), '2', '2026-10-04T12:00:00.000000000Z')`)
	dst, raw := destination(t)
	_, err := Run(context.Background(), src, dst, Options{ReplaceInvalidText: true})
	if err == nil || !strings.Contains(err.Error(), "two rows with key") {
		t.Fatalf("Run = %v, want a refusal of the duplicate key", err)
	}
	assertEmpty(t, raw)
}
