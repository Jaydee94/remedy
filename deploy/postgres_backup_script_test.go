package deploy_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The backup script of the CronJob, as rendered, run with sh against a fake pg_dump in a temporary directory that stands
// in for /backup. It needs sh, date and a find with -mmin and -delete (GNU in the image; BSD find on macOS has both).

// backupScript renders the CronJob and returns its script with /backup replaced by dir, and its RETENTION_MINUTES.
func backupScript(t *testing.T, dir string) (string, string) {
	t.Helper()
	c := container(t, backupPodSpec(t, backupCronJob(t, mustRender(t, bundledValues()))), "backup")
	cmd := listOf(c["command"])
	if len(cmd) != 3 || cmd[0] != "sh" || cmd[1] != "-c" {
		t.Fatalf("the backup command is %v, want sh -c <script>", cmd)
	}
	script := cmd[2].(string)
	if !strings.Contains(script, "/backup/") {
		t.Fatalf("the script does not write to /backup:\n%s", script)
	}
	return strings.ReplaceAll(script, "/backup", dir), env(c)["RETENTION_MINUTES"]["value"].(string)
}

// fakePgDump writes a pg_dump into bin. It records which files the backup directory holds while it runs, writes "dump" to
// its --file argument, and fails after writing when fail is true (a dump cut off midway).
func fakePgDump(t *testing.T, bin, dir string, fail bool) {
	t.Helper()
	exit := "0"
	if fail {
		exit = "1"
	}
	script := `#!/bin/sh
ls "` + dir + `" > "` + bin + `/seen-during-dump"
for a in "$@"; do
  case "$a" in --file=*) printf 'dump' > "${a#--file=}" ;; esac
done
exit ` + exit + "\n"
	if err := os.WriteFile(filepath.Join(bin, "pg_dump"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// seedDumps makes an old dump (10 days), a young one (1 day) and a stale .part (2 days) in dir.
func seedDumps(t *testing.T, dir string) {
	t.Helper()
	now := time.Now()
	for name, age := range map[string]time.Duration{
		"remedy-20260101T000000Z.dump":      10 * 24 * time.Hour,
		"remedy-20260110T000000Z.dump":      24 * time.Hour,
		"remedy-20260105T000000Z.dump.part": 48 * time.Hour,
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
}

func files(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func runBackup(t *testing.T, fail bool) (dir string, out string, err error) {
	t.Helper()
	dir, bin := t.TempDir(), t.TempDir()
	script, retention := backupScript(t, dir)
	if retention != "10080" {
		t.Fatalf("RETENTION_MINUTES = %s, want 10080 (7 days)", retention)
	}
	seedDumps(t, dir)
	fakePgDump(t, bin, dir, fail)
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "RETENTION_MINUTES="+retention, "BACKUP_RETRY_SECONDS=0")
	raw, err := cmd.CombinedOutput()
	seen, _ := os.ReadFile(filepath.Join(bin, "seen-during-dump"))
	if !strings.Contains(string(seen), "remedy-20260101T000000Z.dump") {
		t.Errorf("the old dump was gone while pg_dump ran (it must be deleted only after a new dump): %q", seen)
	}
	return dir, string(raw), err
}

func TestTheBackupScriptKeepsEverythingWhenTheDumpFails(t *testing.T) {
	dir, out, err := runBackup(t, true)
	if err == nil {
		t.Fatalf("the script exited 0 after pg_dump failed:\n%s", out)
	}
	got := files(t, dir)
	want := []string{"remedy-20260101T000000Z.dump", "remedy-20260105T000000Z.dump.part", "remedy-20260110T000000Z.dump"}
	if !equalStrings(got, want) {
		t.Errorf("after a failed dump the directory holds %v, want exactly the old files %v (no new .part, nothing deleted)", got, want)
	}
	if !strings.Contains(out, "pg_dump failed") {
		t.Errorf("the output does not say that pg_dump failed:\n%s", out)
	}
}

func TestTheBackupScriptWritesTheDumpThenDeletesOnlyOldOnes(t *testing.T) {
	dir, out, err := runBackup(t, false)
	if err != nil {
		t.Fatalf("the script failed: %v\n%s", err, out)
	}
	got := files(t, dir)
	if len(got) != 2 || got[0] != "remedy-20260110T000000Z.dump" {
		t.Fatalf("the directory holds %v, want the young dump and one new dump (the 10-day dump and the stale .part deleted)", got)
	}
	fresh := got[1]
	if !strings.HasPrefix(fresh, "remedy-") || !strings.HasSuffix(fresh, "Z.dump") || len(fresh) != len("remedy-20260110T000000Z.dump") {
		t.Errorf("the new dump is called %q, want remedy-<UTC timestamp>.dump", fresh)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, fresh)); string(b) != "dump" {
		t.Errorf("the new dump holds %q, want what pg_dump wrote", b)
	}
	info, err := os.Stat(filepath.Join(dir, fresh))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the new dump's mode = %v, want 0600 (umask 077)", info.Mode().Perm())
	}
}

// fakeFlakyPgDump writes a pg_dump that refuses the connection (nothing written, exit 1) for its first `failures` calls and
// dumps after that. It counts its calls in bin/calls.
func fakeFlakyPgDump(t *testing.T, bin string, failures int) {
	t.Helper()
	script := `#!/bin/sh
n=0; [ -f "` + bin + `/calls" ] && n=$(cat "` + bin + `/calls")
n=$((n + 1)); echo $n > "` + bin + `/calls"
if [ $n -le ` + strconv.Itoa(failures) + ` ]; then
  echo 'pg_dump: error: connection to server failed: Connection refused' >&2
  exit 1
fi
for a in "$@"; do
  case "$a" in --file=*) printf 'dump' > "${a#--file=}" ;; esac
done
`
	if err := os.WriteFile(filepath.Join(bin, "pg_dump"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// runFlaky runs the rendered script against a pg_dump that fails `failures` times, with `attempts` allowed and no sleep.
func runFlaky(t *testing.T, failures, attempts int) (dir, out string, calls int, err error) {
	t.Helper()
	dir, bin := t.TempDir(), t.TempDir()
	script, retention := backupScript(t, dir)
	seedDumps(t, dir)
	fakeFlakyPgDump(t, bin, failures)
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"RETENTION_MINUTES="+retention, "BACKUP_ATTEMPTS="+strconv.Itoa(attempts), "BACKUP_RETRY_SECONDS=0")
	raw, err := cmd.CombinedOutput()
	b, _ := os.ReadFile(filepath.Join(bin, "calls"))
	calls, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	return dir, string(raw), calls, err
}

// A pod that has only just started may not reach the database yet (the home server's k3s refused the connection for the
// first seconds of a new pod: three attempts in 30 s failed on two nights). The script tries again before it gives up.
func TestTheBackupScriptWaitsUntilTheDatabaseAcceptsTheConnection(t *testing.T) {
	dir, out, calls, err := runFlaky(t, 2, 5)
	if err != nil {
		t.Fatalf("the script failed although the third attempt works: %v\n%s", err, out)
	}
	if calls != 3 {
		t.Errorf("pg_dump was called %d times, want 3 (two refused, one dump)", calls)
	}
	got := files(t, dir)
	if len(got) != 2 || got[0] != "remedy-20260110T000000Z.dump" {
		t.Errorf("the directory holds %v, want the young dump and one new dump", got)
	}
}

func TestTheBackupScriptGivesUpAfterItsAttemptsAndKeepsEverything(t *testing.T) {
	dir, out, calls, err := runFlaky(t, 100, 3)
	if err == nil {
		t.Fatalf("the script exited 0 although every attempt failed:\n%s", out)
	}
	if calls != 3 {
		t.Errorf("pg_dump was called %d times, want exactly the 3 attempts allowed", calls)
	}
	want := []string{"remedy-20260101T000000Z.dump", "remedy-20260105T000000Z.dump.part", "remedy-20260110T000000Z.dump"}
	if got := files(t, dir); !equalStrings(got, want) {
		t.Errorf("after the last failed attempt the directory holds %v, want exactly the old files %v", got, want)
	}
	if !strings.Contains(out, "pg_dump failed") {
		t.Errorf("the output does not say that pg_dump failed:\n%s", out)
	}
}

// The defaults cover more than the five minutes after which kube-router (the network policy engine of k3s) has learned a new
// pod: 30 attempts, 20 seconds apart.
func TestTheBackupScriptDefaultsWaitLongerThanFiveMinutes(t *testing.T) {
	script, _ := backupScript(t, t.TempDir())
	re := regexp.MustCompile(`BACKUP_ATTEMPTS:-(\d+)\}.*\n?(?s:.*)BACKUP_RETRY_SECONDS:-(\d+)\}`)
	m := re.FindStringSubmatch(script)
	if m == nil {
		t.Fatalf("the script has no BACKUP_ATTEMPTS and BACKUP_RETRY_SECONDS defaults:\n%s", script)
	}
	attempts, _ := strconv.Atoi(m[1])
	seconds, _ := strconv.Atoi(m[2])
	if attempts*seconds < 600 {
		t.Errorf("the script waits %d s in total, want at least 600", attempts*seconds)
	}
}
