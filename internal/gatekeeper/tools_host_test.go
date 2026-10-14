package gatekeeper_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/hostexec"
	"github.com/Jaydee94/remedy/internal/store"
)

type fakeExec struct {
	runID, host string
	sudo        bool
	argv        []string
}

// fakeHosts stands in for the implementation that holds the logins: it records what it was asked to run.
type fakeHosts struct {
	mu     sync.Mutex
	infos  []gatekeeper.HostInfo
	result hostexec.Result
	err    error
	execs  []fakeExec
}

func (f *fakeHosts) Hosts(context.Context, string) ([]gatekeeper.HostInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.infos), nil
}

func (f *fakeHosts) Exec(_ context.Context, runID, host string, sudo bool, argv []string) (hostexec.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execs = append(f.execs, fakeExec{runID, host, sudo, slices.Clone(argv)})
	return f.result, f.err
}

func (f *fakeHosts) execCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.execs)
}

func (f *fakeHosts) set(change func(f *fakeHosts)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func nasInfo() gatekeeper.HostInfo {
	return gatekeeper.HostInfo{Name: "nas", Notes: "The NAS. Paperless lives here.", DockerPattern: "remedy-*", Units: []string{"node_exporter.service"}}
}

// hostRunToken creates hosts, a run that has them, claims it and returns its token. With no names the run has tools but no hosts.
func hostRunToken(t *testing.T, e *env, names ...string) string {
	t.Helper()
	ctx := context.Background()
	var ids []int64
	for _, n := range names {
		h, err := e.st.CreateHost(ctx, store.HostFields{Name: n, Address: "127.0.0.1", Port: 22, Account: "remedy", Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, h.ID)
	}
	if _, err := e.st.CreateRunWith(ctx, store.RunOptions{Provider: "claude", Prompt: "look at the host", Tools: true, HostIDs: ids}); err != nil {
		t.Fatal(err)
	}
	claimed, err := e.st.ClaimNext(ctx)
	if err != nil || claimed == nil || claimed.HostCount != len(ids) {
		t.Fatalf("ClaimNext = %+v, %v", claimed, err)
	}
	token, err := e.st.MintRunToken(ctx, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func newHostRig(t *testing.T, fake *fakeHosts) *env {
	t.Helper()
	e := newEnvWith(t, func(*store.Store) []gatekeeper.Tool { return gatekeeper.HostTools(fake) }, func(c *gatekeeper.Config) {
		c.ProgressInterval = 20 * time.Millisecond
		c.Grace = testGrace
	})
	e.token = hostRunToken(t, e, "nas")
	return e
}

func newFake() *fakeHosts {
	return &fakeHosts{infos: []gatekeeper.HostInfo{nasInfo()}, result: hostexec.Result{Stdout: "CONTAINER ID  NAMES\nabc  remedy-drill\n"}}
}

// askHost makes a call and returns the text of the answer. useID must be different for every call of a test.
func askHost(t *testing.T, e *env, useID, tool string, args any) (string, bool) {
	t.Helper()
	return resultText(t, e.call(t, useID, tool, args))
}

func hostTool(t *testing.T, a gatekeeper.HostAccess, name string) gatekeeper.Tool {
	t.Helper()
	for _, tool := range gatekeeper.HostTools(a) {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("no host tool %s", name)
	return gatekeeper.Tool{}
}

func TestTheHostToolsAreInTheirGroupAndOnlyTheChangeNeedsAnApproval(t *testing.T) {
	tools := gatekeeper.HostTools(nil)
	want := []string{"host_list", "host_read", "host_change"}
	if len(tools) != len(want) {
		t.Fatalf("%d host tools", len(tools))
	}
	for i, tool := range tools {
		if tool.Name != want[i] || tool.Group != gatekeeper.GroupHost || !json.Valid(tool.Schema) {
			t.Errorf("%s: group %q, schema valid %v", tool.Name, tool.Group, json.Valid(tool.Schema))
		}
		isChange := tool.Name == "host_change"
		if tool.Mutating != isChange || (isChange && (tool.CheckRun == nil || tool.Activity == nil || tool.ActivityKind != store.KindHostAction)) {
			t.Errorf("%s: mutating %v, checkRun %v, activity %v, kind %q", tool.Name, tool.Mutating, tool.CheckRun != nil, tool.Activity != nil, tool.ActivityKind)
		}
	}
	if !strings.Contains(tools[2].Description, "maintainer has to approve") {
		t.Error("host_change does not say that it waits for an approval")
	}
	for _, form := range hostexec.ReadForms() {
		if !strings.Contains(tools[1].Description, form) {
			t.Errorf("host_read does not name the form %q", form)
		}
	}
}

func TestOnlyARunWithHostsIsOfferedTheHostTools(t *testing.T) {
	e := newHostRig(t, newFake())
	with := e.token
	without := hostRunToken(t, e) // a tools run that has no hosts

	if got := listedTools(t, e, with); !slices.Contains(got, "host_list") || !slices.Contains(got, "host_read") || !slices.Contains(got, "host_change") {
		t.Fatalf("a run with hosts is offered %v", got)
	}
	if got := listedTools(t, e, without); slices.Contains(got, "host_list") || slices.Contains(got, "host_read") || slices.Contains(got, "host_change") {
		t.Fatalf("a run without hosts is offered %v", got)
	}
	// Not offered means: answered like a tool that does not exist.
	e.token = without
	if text, isErr := askHost(t, e, "toolu_x", "host_read", map[string]any{"host": "nas", "argv": []string{"uptime"}}); !isErr || !strings.Contains(text, "unknown tool") {
		t.Fatalf("a run without hosts calling host_read: %q (%v)", text, isErr)
	}
}

func TestHostListShowsTheRulesAndNoAddress(t *testing.T) {
	fake := newFake()
	fake.set(func(f *fakeHosts) { f.infos[0].SudoNone = true })
	e := newHostRig(t, fake)
	text, isErr := askHost(t, e, "toolu_1", "host_list", map[string]any{})
	if isErr {
		t.Fatal(text)
	}
	for _, want := range []string{"nas", "Paperless lives here", "remedy-*", "node_exporter.service", "sudo: does not work"} {
		if !strings.Contains(text, want) {
			t.Errorf("host_list lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "127.0.0.1") {
		t.Errorf("host_list shows an address:\n%s", text)
	}
}

func TestHostReadRunsAFormOfTheTableWithoutAnApproval(t *testing.T) {
	fake := newFake()
	e := newHostRig(t, fake)
	text, isErr := askHost(t, e, "toolu_1", "host_read", map[string]any{"host": "nas", "argv": []string{"docker", "ps", "-a"}})
	if isErr || !strings.Contains(text, hostexec.DataLine("nas")) || !strings.Contains(text, "exit code: 0") || !strings.Contains(text, "remedy-drill") {
		t.Fatalf("answer = %q (%v)", text, isErr)
	}
	if _, isErr := askHost(t, e, "toolu_2", "host_read", map[string]any{"host": "NAS", "argv": []string{"df", "-h"}}); isErr {
		t.Fatal("the host name is matched without regard to case")
	}
	noApproval(t, e)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.execs) != 2 || fake.execs[0].host != "nas" || !fake.execs[0].sudo || fake.execs[1].sudo ||
		!slices.Equal(fake.execs[0].argv, []string{"docker", "ps", "-a"}) {
		t.Fatalf("execs = %+v: docker reads run with sudo, the others without", fake.execs)
	}
	if fake.execs[1].host != "nas" {
		t.Fatalf("the command ran on %q: the host is the one the table lists, not the spelling of the agent", fake.execs[1].host)
	}
}

func TestHostReadRefusesWhatIsNotInTheTableAndSendsNothing(t *testing.T) {
	fake := newFake()
	e := newHostRig(t, fake)
	for name, c := range map[string]struct {
		host string
		argv []string
		want string
	}{
		"a change":                        {"nas", []string{"docker", "restart", "remedy-a"}, "host_change"},
		"a file reader":                   {"nas", []string{"cat", "/etc/hostname"}, "host_change"},
		"a form with an extra flag":       {"nas", []string{"df", "-h", "/"}, "host_change"},
		"a container outside the pattern": {"nas", []string{"docker", "logs", "--tail", "50", "other"}, "does not match"},
		"a never-listed command":          {"nas", []string{"rm", "-rf", "/"}, "never run"},
		"a shell":                         {"nas", []string{"sh", "-c", "id"}, "never run"},
		"a host the run does not have":    {"other", []string{"uptime"}, "no host"},
	} {
		text, isErr := askHost(t, e, "toolu_"+strings.ReplaceAll(name, " ", "_"), "host_read", map[string]any{"host": c.host, "argv": c.argv})
		if !isErr || !strings.Contains(text, c.want) {
			t.Errorf("%s: answer = %q (%v), want an error that says %q", name, text, isErr, c.want)
		}
	}
	if n := fake.execCount(); n != 0 {
		t.Fatalf("%d commands reached the host", n)
	}
	// The refusal names the forms, so that the agent can use one.
	text, _ := askHost(t, e, "toolu_forms", "host_read", map[string]any{"host": "nas", "argv": []string{"ls"}})
	if !strings.Contains(text, "docker ps -a") || !strings.Contains(text, "journalctl -u") {
		t.Errorf("the refusal does not list the forms:\n%s", text)
	}
}

func TestHostReadRefusesBadWordsAndSendsNothing(t *testing.T) {
	fake := newFake()
	e := newHostRig(t, fake)
	for name, argv := range map[string][]string{
		"a newline":     {"df", "-h\n"},
		"a NUL":         {"df", "-h\x00"},
		"an escape":     {"df", "\x1b[31m"},
		"an empty word": {"df", ""},
		"invalid UTF-8": {"df", "\xff"},
		"no words":      {},
		"a long word":   {strings.Repeat("x", 2000)},
	} {
		text, isErr := askHost(t, e, "toolu_"+strings.ReplaceAll(name, " ", "_"), "host_read", map[string]any{"host": "nas", "argv": argv})
		if !isErr || strings.Contains(text, "the tool failed") {
			t.Errorf("%s: answer = %q (%v): the agent is told what to fix", name, text, isErr)
		}
	}
	if n := fake.execCount(); n != 0 {
		t.Fatalf("%d commands reached the host", n)
	}
	for i, bad := range []map[string]any{
		{"host": "nas"},
		{"argv": []string{"uptime"}},
		{"host": "nas", "argv": []string{"uptime"}, "extra": 1},
		{"host": "../x", "argv": []string{"uptime"}},
		{"host": "nas", "argv": "uptime"},
	} {
		if text, isErr := askHost(t, e, fmt.Sprintf("toolu_bad_%d", i), "host_read", bad); !isErr {
			t.Errorf("%v: answer = %q: arguments that do not fit must be refused", bad, text)
		}
	}
	if n := fake.execCount(); n != 0 {
		t.Fatalf("%d commands reached the host", n)
	}
}

func TestDockerInspectNeverShowsEnvironmentValues(t *testing.T) {
	fake := newFake()
	fake.set(func(f *fakeHosts) {
		f.result = hostexec.Result{Stdout: `[{"Id":"abc","Config":{"Env":["DB_PASSWORD=hunter2","PATH=/usr/bin"]},"State":{"Running":true}}]`}
	})
	e := newHostRig(t, fake)
	text, isErr := askHost(t, e, "toolu_1", "host_read", map[string]any{"host": "nas", "argv": []string{"docker", "inspect", "remedy-a"}})
	if isErr || strings.Contains(text, "hunter2") || !strings.Contains(text, "DB_PASSWORD=") || !strings.Contains(text, "PATH=<value removed>") || !strings.Contains(text, `"Running": true`) {
		t.Fatalf("filtered answer = %q (%v)", text, isErr)
	}
	for name, res := range map[string]hostexec.Result{
		"output that is not JSON": {Stdout: "Error: something HUNTER3"},
		"a cut document":          {Stdout: `[{"Config":{"Env":["A=HUNTER4"`, StdoutCut: true},
		"an env of another shape": {Stdout: `[{"Config":{"Env":"A=HUNTER5"}}]`},
		"a complete cut document": {Stdout: `[{"Config":{"Env":["A=HUNTER6"]}}]`, StdoutCut: true},
	} {
		fake.set(func(f *fakeHosts) { f.result = res })
		text, _ := askHost(t, e, "toolu_"+strings.ReplaceAll(name, " ", "_"), "host_read", map[string]any{"host": "nas", "argv": []string{"docker", "inspect", "remedy-a"}})
		if strings.Contains(text, "HUNTER") || !strings.Contains(text, "could not be filtered") {
			t.Errorf("%s: %q", name, text)
		}
	}
	// A failing inspect keeps its error message on stderr.
	fake.set(func(f *fakeHosts) { f.result = hostexec.Result{Stderr: "Error: No such object: x", ExitCode: 1} })
	if text, _ := askHost(t, e, "toolu_err", "host_read", map[string]any{"host": "nas", "argv": []string{"docker", "inspect", "remedy-a"}}); !strings.Contains(text, "No such object") {
		t.Errorf("the error of a failing inspect is lost: %q", text)
	}
}

func TestTheResultOfAHostIsRedacted(t *testing.T) {
	fake := newFake()
	fake.set(func(f *fakeHosts) {
		f.result = hostexec.Result{Stdout: "token ghp_DISTINCTIVE0123456789abcdefghijklmnopqrst here"}
	})
	e := newHostRig(t, fake)
	text, isErr := askHost(t, e, "toolu_1", "host_read", map[string]any{"host": "nas", "argv": []string{"uptime"}})
	if isErr || strings.Contains(text, "ghp_DISTINCTIVE") {
		t.Fatalf("answer = %q (%v)", text, isErr)
	}
}

// What a host prints is not trusted to be UTF-8 or to be short: the tool makes it valid text and the gatekeeper limits it.
func TestTheResultOfAHostIsValidTextAndLimited(t *testing.T) {
	fake := newFake()
	fake.set(func(f *fakeHosts) { f.result = hostexec.Result{Stdout: "ok \xff\xfe bad \xc3", Stderr: "err \x80"} })
	read := hostTool(t, fake, "host_read")
	text, err := read.Run(context.Background(), gatekeeper.Call{RunID: "r", Args: json.RawMessage(`{"host":"nas","argv":["uptime"]}`)})
	if err != nil || !utf8.ValidString(text) || !strings.Contains(text, "ok ") {
		t.Fatalf("Run = %q, %v: the text must be valid UTF-8", text, err)
	}

	big := strings.Repeat("0123456789abcdef", 1500) // 24000 bytes on each stream
	fake.set(func(f *fakeHosts) { f.result = hostexec.Result{Stdout: big, Stderr: big} })
	e := newHostRig(t, fake)
	answer, isErr := askHost(t, e, "toolu_big", "host_read", map[string]any{"host": "nas", "argv": []string{"uptime"}})
	if isErr || len(answer) > gatekeeper.MaxResultBytes+100 || !strings.Contains(answer, "[cut: the result is longer than 32 KB]") {
		t.Fatalf("a result of %d bytes (%v) is not limited", len(answer), isErr)
	}
}

func TestHostChangeWaitsForTheApprovalAndThenRunsWithSudo(t *testing.T) {
	fake := newFake()
	e := newHostRig(t, fake)
	s := e.open(t, "toolu_1", "host_change", map[string]any{"host": "nas", "argv": []string{"docker", "restart", "remedy-drill"}})
	call := e.pending(t)
	if string(call.Arguments) != `{"host":"nas","argv":["docker","restart","remedy-drill"]}` || call.Kind != store.CallKindMutating {
		t.Fatalf("pending call = %+v", call)
	}
	if n := fake.execCount(); n != 0 {
		t.Fatalf("%d commands ran before the approval", n)
	}
	if _, err := e.g.Decide(context.Background(), call.ID, true, "go on"); err != nil {
		t.Fatal(err)
	}
	text, isErr := s.result()
	if isErr || !strings.Contains(text, hostexec.DataLine("nas")) || !strings.Contains(text, "exit code: 0") {
		t.Fatalf("answer = %q (%v)", text, isErr)
	}
	fake.mu.Lock()
	if len(fake.execs) != 1 || !fake.execs[0].sudo || !slices.Equal(fake.execs[0].argv, []string{"docker", "restart", "remedy-drill"}) {
		t.Fatalf("execs = %+v", fake.execs)
	}
	fake.mu.Unlock()
	acts := activityOf(t, e.st, store.KindHostAction)
	if len(acts) != 1 || acts[0].Summary != "Ran docker restart remedy-drill on nas" {
		t.Fatalf("activity = %+v", acts)
	}
}

// The words the maintainer reads on the card are the words that run: the stored arguments are the one source of both.
func TestWhatIsShownForTheApprovalIsWhatRuns(t *testing.T) {
	fake := newFake()
	e := newHostRig(t, fake)
	argv := []string{"echo", "it's a b", `say "x"`}
	s := e.open(t, "toolu_1", "host_change", map[string]any{"host": "nas", "argv": argv})
	call := e.pending(t)
	var stored struct {
		Host string
		Argv []string
	}
	if err := json.Unmarshal(call.Arguments, &stored); err != nil || !slices.Equal(stored.Argv, argv) {
		t.Fatalf("stored = %s, %v: the card is built from the stored arguments, which must be the words as sent", call.Arguments, err)
	}
	if _, err := e.g.Decide(context.Background(), call.ID, true, "go on"); err != nil {
		t.Fatal(err)
	}
	if text, isErr := s.result(); isErr {
		t.Fatal(text)
	}
	fake.mu.Lock()
	ran := slices.Clone(fake.execs)
	fake.mu.Unlock()
	if len(ran) != 1 || !slices.Equal(ran[0].argv, stored.Argv) {
		t.Fatalf("ran %+v, approved %v", ran, stored.Argv)
	}
	acts := activityOf(t, e.st, store.KindHostAction)
	if len(acts) != 1 || acts[0].Summary != "Ran "+hostexec.Display(ran[0].argv)+" on nas" {
		t.Fatalf("activity = %+v", acts)
	}
}

// Display does not escape characters that are invisible or look like others (a right-to-left override, a no-break space),
// so a change that has one is refused: the card must show exactly what runs.
func TestAChangeWithInvisibleCharactersIsRefused(t *testing.T) {
	fake := newFake()
	e := newHostRig(t, fake)
	for name, word := range map[string]string{
		"a right-to-left override": "remedy-a\u202e",
		"a zero-width space":       "remedy\u200b-a",
		"a no-break space":         "remedy\u00a0a",
		"a line separator":         "remedy\u2028a",
		"a private use character":  "remedy\ue000a",
	} {
		text, isErr := askHost(t, e, "toolu_"+strings.ReplaceAll(name, " ", "_"), "host_change", map[string]any{"host": "nas", "argv": []string{"echo", word}})
		if !isErr || !strings.Contains(text, "not printable") {
			t.Errorf("%s: answer = %q (%v)", name, text, isErr)
		}
	}
	// Letters of other scripts are fine.
	s := e.open(t, "toolu_ok", "host_change", map[string]any{"host": "nas", "argv": []string{"echo", "größe 日本"}})
	call := e.pending(t)
	if _, err := e.g.Decide(context.Background(), call.ID, false, "no"); err != nil {
		t.Fatal(err)
	}
	s.result()
	if n := fake.execCount(); n != 0 {
		t.Fatalf("%d commands reached the host", n)
	}
}

// approveChange makes a host_change call, approves it and returns the answer.
func approveChange(t *testing.T, e *env, useID string, argv ...string) (string, bool) {
	t.Helper()
	s := e.open(t, useID, "host_change", map[string]any{"host": "nas", "argv": argv})
	call := e.pending(t)
	if _, err := e.g.Decide(context.Background(), call.ID, true, "go on"); err != nil {
		t.Fatal(err)
	}
	return s.result()
}

// An approved docker inspect is filtered like the one of host_read: one approval must not show the values of the environment.
func TestAnApprovedInspectNeverShowsEnvironmentValues(t *testing.T) {
	fake := newFake()
	fake.set(func(f *fakeHosts) {
		f.result = hostexec.Result{Stdout: `[{"Id":"abc","Config":{"Env":["DB_PASSWORD=hunter2","PATH=/usr/bin"]},"State":{"Running":true}}]`}
	})
	e := newHostRig(t, fake)
	for i, argv := range [][]string{
		{"docker", "inspect", "remedy-a"},
		{"docker", "container", "inspect", "remedy-a"},
	} {
		text, isErr := approveChange(t, e, fmt.Sprintf("toolu_ok_%d", i), argv...)
		if isErr || strings.Contains(text, "hunter2") || !strings.Contains(text, "PATH=<value removed>") {
			t.Errorf("%v: answer = %q (%v)", argv, text, isErr)
		}
	}
	// A format string prints something that is not the list of objects: the fixed text, never the raw output.
	fake.set(func(f *fakeHosts) { f.result = hostexec.Result{Stdout: `["DB_PASSWORD=HUNTER9"]` + "\n"} })
	text, isErr := approveChange(t, e, "toolu_fmt", "docker", "inspect", "--format", "{{json .Config.Env}}", "remedy-a")
	if isErr || strings.Contains(text, "HUNTER9") || !strings.Contains(text, "could not be filtered") {
		t.Errorf("a format string: answer = %q (%v)", text, isErr)
	}
}

// A template can put the environment under any key, which a filter that knows only Config.Env cannot see: the output of
// an inspect with a format is never shown, whatever it looks like.
func TestAnInspectWithAFormatIsNeverShown(t *testing.T) {
	fake := newFake()
	fake.set(func(f *fakeHosts) { f.result = hostexec.Result{Stdout: `[{"x":["DB_PASSWORD=HUNTER9"]}]`} })
	e := newHostRig(t, fake)
	for i, argv := range [][]string{
		{"docker", "inspect", "remedy-a", "--format", `[{"x":{{json .Config.Env}}}]`},
		{"docker", "inspect", "-f", `[{"x":{{json .Config.Env}}}]`, "remedy-a"},
		{"docker", "inspect", `--format=[{"x":{{json .Config.Env}}}]`, "remedy-a"},
		{"docker", "inspect", `-f[{"x":{{json .Config.Env}}}]`, "remedy-a"},
		{"docker", "container", "inspect", "-sf", `[{"x":{{json .Config.Env}}}]`, "remedy-a"},
		{"docker", "-H", "x", "inspect", `--format=[{"x":{{json .Config.Env}}}]`, "remedy-a"},
	} {
		text, isErr := approveChange(t, e, fmt.Sprintf("toolu_f_%d", i), argv...)
		if isErr || strings.Contains(text, "HUNTER9") || !strings.Contains(text, "could not be filtered") {
			t.Errorf("%q: answer = %q (%v)", argv, text, isErr)
		}
	}
	// Without a format the filter decides, and a document of the right shape is shown, filtered.
	fake.set(func(f *fakeHosts) {
		f.result = hostexec.Result{Stdout: `[{"ContainerConfig":{"Env":["DB_PASSWORD=HUNTER8"]}}]`}
	})
	if text, _ := approveChange(t, e, "toolu_cc", "docker", "inspect", "remedy-a"); strings.Contains(text, "HUNTER8") || !strings.Contains(text, "DB_PASSWORD=") {
		t.Errorf("ContainerConfig: %q", text)
	}
}

// The activity entry is built from the agent's words: it is clipped and redacted like a result.
func TestTheActivityOfAChangeIsClippedAndRedacted(t *testing.T) {
	fake := newFake()
	e := newHostRig(t, fake)
	approveChange(t, e, "toolu_long", "echo", strings.Repeat("x", 600))
	approveChange(t, e, "toolu_secret", "echo", "ghp_DISTINCTIVE0123456789abcdefghijklmnopqrst")
	acts := activityOf(t, e.st, store.KindHostAction)
	if len(acts) != 2 {
		t.Fatalf("activity = %+v", acts)
	}
	long, secret := acts[0].Summary, acts[1].Summary
	if strings.Contains(secret, "xxxx") {
		long, secret = secret, long // the newest entry comes first
	}
	if len(long) > len("Ran ")+200+len("...")+len(" on nas") || !strings.HasSuffix(long, "... on nas") {
		t.Errorf("a long command is not clipped (%d bytes): %q", len(long), long)
	}
	if strings.Contains(secret, "ghp_DISTINCTIVE") || !strings.HasPrefix(secret, "Ran echo ") {
		t.Errorf("a secret in the command reached the activity log: %q", secret)
	}
}

// A change in the second the question was asked in counts as after it: the time of the call has second resolution.
func TestAHostChangedInTheSecondOfTheQuestionIsStale(t *testing.T) {
	fake := newFake()
	change := hostTool(t, fake, "host_change")
	asked := time.Date(2026, 10, 13, 12, 0, 0, 700_000_000, time.UTC)
	args := json.RawMessage(`{"host":"nas","argv":["docker","restart","remedy-a"]}`)
	for name, c := range map[string]struct {
		changed time.Time
		runs    bool
	}{
		"in the same second, before": {asked.Add(-600 * time.Millisecond), false},
		"in the same second, after":  {asked.Add(200 * time.Millisecond), false},
		"well before the question":   {asked.Add(-time.Minute), true},
		"in the second before":       {time.Date(2026, 10, 13, 11, 59, 59, 999_000_000, time.UTC), true},
	} {
		fake.set(func(f *fakeHosts) { f.execs = nil; f.infos[0].ChangedAt = c.changed })
		_, err := change.Run(context.Background(), gatekeeper.Call{RunID: "r", Args: args, RequestedAt: asked})
		if (err == nil) != c.runs {
			t.Errorf("%s: Run = %v, want it to run: %v", name, err, c.runs)
		}
	}
}

func TestAnApprovedChangeIsNotRunWhereSudoDoesNotWork(t *testing.T) {
	fake := newFake()
	fake.set(func(f *fakeHosts) { f.infos[0].SudoNone = true })
	change := hostTool(t, fake, "host_change")
	args := json.RawMessage(`{"host":"nas","argv":["docker","restart","remedy-a"]}`)
	_, err := change.Run(context.Background(), gatekeeper.Call{RunID: "r", Args: args, RequestedAt: time.Now()})
	var arg gatekeeper.ArgumentError
	if !errors.As(err, &arg) || !strings.Contains(err.Error(), "sudo cannot run") || fake.execCount() != 0 {
		t.Fatalf("Run = %v (%d commands sent)", err, fake.execCount())
	}
}

// The notes are the maintainer's text, but a line break in them must not look like a new entry of the list.
func TestHostNotesCannotImitateTheStructureOfTheList(t *testing.T) {
	fake := newFake()
	fake.set(func(f *fakeHosts) {
		f.infos[0].Notes = "fine\n\nother-host\n  containers: *\r\n  units: all   sudo: works\tend"
	})
	list := hostTool(t, fake, "host_list")
	text, err := list.Run(context.Background(), gatekeeper.Call{RunID: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(text, "\n  containers:") != 1 || strings.Count(text, "\n  units:") != 1 || strings.ContainsAny(text, "\r\t ") {
		t.Fatalf("a note imitates the list:\n%q", text)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "other-host") {
			t.Fatalf("a note starts a line of its own: %q", text)
		}
	}
}

func TestAChangeThatCannotWorkIsRefusedBeforeAnApprovalIsAskedFor(t *testing.T) {
	fake := newFake()
	e := newHostRig(t, fake)
	for name, c := range map[string]struct {
		args map[string]any
		want string
	}{
		"a container outside the pattern":  {map[string]any{"host": "nas", "argv": []string{"docker", "restart", "other"}}, "does not match"},
		"a container that is only hex":     {map[string]any{"host": "nas", "argv": []string{"docker", "restart", "abc123"}}, "does not match"},
		"a unit the host does not list":    {map[string]any{"host": "nas", "argv": []string{"systemctl", "restart", "unknown.service"}}, "not one this host lists"},
		"a unit without its suffix":        {map[string]any{"host": "nas", "argv": []string{"systemctl", "restart", "node_exporter"}}, "suffix"},
		"a never-listed command":           {map[string]any{"host": "nas", "argv": []string{"rm", "-rf", "/"}}, "never runs this command"},
		"a shell":                          {map[string]any{"host": "nas", "argv": []string{"bash", "-c", "id"}}, "never runs this command"},
		"an option of sudo as the program": {map[string]any{"host": "nas", "argv": []string{"-s", "docker"}}, "first word"},
		"an environment assignment":        {map[string]any{"host": "nas", "argv": []string{"FOO=bar", "docker", "ps"}}, "first word"},
		"a relative program":               {map[string]any{"host": "nas", "argv": []string{"./x"}}, "first word"},
		"a host the run does not have":     {map[string]any{"host": "other", "argv": []string{"docker", "restart", "remedy-a"}}, "no host"},
		"a newline":                        {map[string]any{"host": "nas", "argv": []string{"docker", "restart", "a\nb"}}, "control character"},
	} {
		text, isErr := askHost(t, e, "toolu_"+strings.ReplaceAll(name, " ", "_"), "host_change", c.args)
		if !isErr || !strings.Contains(text, c.want) {
			t.Errorf("%s: answer = %q (%v), want an error that says %q", name, text, isErr, c.want)
		}
	}
	fake.set(func(f *fakeHosts) { f.infos[0].SudoNone = true })
	if text, isErr := askHost(t, e, "toolu_nosudo", "host_change", map[string]any{"host": "nas", "argv": []string{"docker", "restart", "remedy-a"}}); !isErr || !strings.Contains(text, "sudo cannot run") {
		t.Errorf("a host where sudo does not work: %q (%v)", text, isErr)
	}
	noApproval(t, e)
	if n := fake.execCount(); n != 0 {
		t.Fatalf("%d commands reached the host", n)
	}
}

func TestADeniedChangeRunsNothing(t *testing.T) {
	fake := newFake()
	e := newHostRig(t, fake)
	s := e.open(t, "toolu_1", "host_change", map[string]any{"host": "nas", "argv": []string{"docker", "restart", "remedy-drill"}})
	call := e.pending(t)
	if _, err := e.g.Decide(context.Background(), call.ID, false, "no"); err != nil {
		t.Fatal(err)
	}
	if text, isErr := s.result(); !isErr {
		t.Fatalf("answer = %q (%v), want an error", text, isErr)
	}
	if n := fake.execCount(); n != 0 {
		t.Fatalf("%d commands ran after a denial", n)
	}
	if acts := activityOf(t, e.st, store.KindHostAction); len(acts) != 0 {
		t.Fatalf("activity = %+v for a change that did not run", acts)
	}
}

func TestAnApprovedChangeIsNotRunWhenTheHostChangedMeanwhile(t *testing.T) {
	fake := newFake()
	e := newHostRig(t, fake)
	s := e.open(t, "toolu_1", "host_change", map[string]any{"host": "nas", "argv": []string{"docker", "restart", "remedy-drill"}})
	call := e.pending(t)
	// The maintainer changes the host (its address, its key or its login) while the question is open.
	fake.set(func(f *fakeHosts) { f.infos[0].ChangedAt = time.Now().Add(time.Minute) })
	if _, err := e.g.Decide(context.Background(), call.ID, true, "go on"); err != nil {
		t.Fatal(err)
	}
	text, isErr := s.result()
	if !isErr || !strings.Contains(text, "changed after the approval was asked for") {
		t.Fatalf("answer = %q (%v)", text, isErr)
	}
	if n := fake.execCount(); n != 0 {
		t.Fatalf("%d commands ran on a host that changed after the question", n)
	}
}

// A call that reached the database some other way is still not run: Run checks the never-list again.
func TestAnApprovedChangeStillHonoursTheNeverList(t *testing.T) {
	fake := newFake()
	change := hostTool(t, fake, "host_change")
	args, _ := json.Marshal(map[string]any{"host": "nas", "argv": []string{"rm", "-rf", "/"}})
	_, err := change.Run(context.Background(), gatekeeper.Call{RunID: "r", Args: args, RequestedAt: time.Now()})
	var arg gatekeeper.ArgumentError
	if !errors.As(err, &arg) || fake.execCount() != 0 {
		t.Fatalf("Run = %v (%d commands sent): a never-listed command must not run, approved or not", err, fake.execCount())
	}
	// Also words that are no words at all.
	args, _ = json.Marshal(map[string]any{"host": "nas", "argv": []string{"docker", "restart", "remedy-a\n"}})
	if _, err := change.Run(context.Background(), gatekeeper.Call{RunID: "r", Args: args, RequestedAt: time.Now()}); !errors.As(err, &arg) || fake.execCount() != 0 {
		t.Fatalf("Run = %v (%d commands sent): words with a control character must not run", err, fake.execCount())
	}
}

func TestAHostDeletedMeanwhileIsNotUsable(t *testing.T) {
	fake := newFake()
	e := newHostRig(t, fake)
	fake.set(func(f *fakeHosts) { f.infos = nil }) // the host was deleted, disabled or lost its login or its pin
	if text, _ := askHost(t, e, "toolu_1", "host_list", map[string]any{}); !strings.Contains(text, "no usable host") {
		t.Errorf("host_list = %q", text)
	}
	if text, isErr := askHost(t, e, "toolu_2", "host_read", map[string]any{"host": "nas", "argv": []string{"uptime"}}); !isErr || !strings.Contains(text, "no host") {
		t.Errorf("host_read = %q (%v)", text, isErr)
	}
	if text, isErr := askHost(t, e, "toolu_3", "host_change", map[string]any{"host": "nas", "argv": []string{"docker", "restart", "remedy-a"}}); !isErr || !strings.Contains(text, "no host") {
		t.Errorf("host_change = %q (%v)", text, isErr)
	}
	noApproval(t, e)
}

func TestAFailureOfTheConnectionIsAFixedSentenceAndNothingElse(t *testing.T) {
	fake := newFake()
	e := newHostRig(t, fake)
	fake.set(func(f *fakeHosts) { f.err = gatekeeper.ArgumentError("the host did not answer in time") })
	if text, isErr := askHost(t, e, "toolu_1", "host_read", map[string]any{"host": "nas", "argv": []string{"uptime"}}); !isErr || text != "the host did not answer in time" {
		t.Errorf("a sentence of the implementation: %q (%v)", text, isErr)
	}
	fake.set(func(f *fakeHosts) {
		f.err = errors.New("dial tcp 10.1.2.3:22: connect: connection refused for user remedy")
	})
	text, isErr := askHost(t, e, "toolu_2", "host_read", map[string]any{"host": "nas", "argv": []string{"uptime"}})
	if !isErr || strings.Contains(text, "10.1.2.3") || strings.Contains(text, "remedy") || !strings.Contains(text, "the tool failed") {
		t.Errorf("an unexpected error reached the agent as %q", text)
	}
}

// The SIGTERM of a command that timed out is best effort, so the change may have happened. The agent is told that, and is
// not told to run it again.
func TestAChangeThatTimedOutHasAnUnknownOutcome(t *testing.T) {
	fake := newFake()
	e := newHostRig(t, fake)
	fake.set(func(f *fakeHosts) { f.err = fmt.Errorf("exec: %w", gatekeeper.ErrHostTimeout) })
	s := e.open(t, "toolu_1", "host_change", map[string]any{"host": "nas", "argv": []string{"docker", "restart", "remedy-drill"}})
	call := e.pending(t)
	if _, err := e.g.Decide(context.Background(), call.ID, true, "go on"); err != nil {
		t.Fatal(err)
	}
	text, isErr := s.result()
	if !isErr || !strings.Contains(text, "did not answer in time") || !strings.Contains(text, "may or may not have") {
		t.Fatalf("answer = %q (%v): a timed-out change has an unknown outcome", text, isErr)
	}
	for _, retry := range []string{"retry", "try again", "run it again", "again"} {
		if strings.Contains(strings.ToLower(text), retry) {
			t.Errorf("the answer suggests a retry (%q): %q", retry, text)
		}
	}
	if acts := activityOf(t, e.st, store.KindHostAction); len(acts) != 0 {
		t.Fatalf("activity = %+v: the change is not known to have run", acts)
	}

	// A read that timed out is simply a read that timed out.
	if text, isErr := askHost(t, e, "toolu_2", "host_read", map[string]any{"host": "nas", "argv": []string{"uptime"}}); !isErr || text != "the host did not answer in time" {
		t.Errorf("a read that timed out: %q (%v)", text, isErr)
	}
}
