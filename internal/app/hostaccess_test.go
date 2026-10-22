package app_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/app"
	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/hostexec"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/sshhost"
	"github.com/Jaydee94/remedy/internal/sshhost/sshtest"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/internal/testutil"
)

type accessRig struct {
	st     *store.Store
	key    secret.Key
	srv    *sshtest.Server
	access *app.HostAccess
	host   store.Host
	pin    string
	runID  string
}

func masterKey(t *testing.T, fill byte) secret.Key {
	t.Helper()
	k, err := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// newAccessRig makes a host with a sealed password login and a pinned key on a test SSH server, and a run that has it.
func newAccessRig(t *testing.T, cmds map[string]sshtest.Reply) *accessRig {
	t.Helper()
	ctx := context.Background()
	st := testutil.Store(t)
	key := masterKey(t, 6)
	srv := sshtest.Start(t, sshtest.Config{Password: "pw", Commands: cmds})
	h, err := st.CreateHost(ctx, store.HostFields{
		Name: "nas", Address: srv.Host, Port: srv.Port, Account: "remedy", Notes: "the NAS",
		DockerPattern: "remedy-*", SystemdUnits: []string{"node_exporter.service"}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := &accessRig{st: st, key: key, srv: srv, host: h}
	r.setPassword(t, "pw")
	k, err := sshhost.Probe(ctx, sshhost.Target{Address: srv.Host, Port: srv.Port, Account: "remedy"})
	if err != nil {
		t.Fatal(err)
	}
	r.pin = k.Line
	if err := st.PinHostKey(ctx, h.ID, k.Line, time.Now()); err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateRunWith(ctx, store.RunOptions{Provider: "claude", Prompt: "p", HostIDs: []int64{h.ID}})
	if err != nil {
		t.Fatal(err)
	}
	r.runID = run.ID
	r.access = &app.HostAccess{Store: st, Key: key, SSH: &sshhost.Service{}}
	return r
}

func (r *accessRig) setPassword(t *testing.T, pw string) {
	t.Helper()
	cred, err := sshhost.NewPasswordCredential(pw)
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := cred.Marshal()
	sealed, err := r.key.Seal(blob, store.HostCredentialAAD(r.host.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.st.SaveHostCredential(context.Background(), store.HostCredential{HostID: r.host.ID, Kind: "password", Ciphertext: sealed, Hint: "set"}); err != nil {
		t.Fatal(err)
	}
}

func okCmds() map[string]sshtest.Reply {
	return map[string]sshtest.Reply{hostexec.CanaryCommand(): {Out: hostexec.CanaryWant()}}
}

func TestHostsLeavesOutWhatCannotBeUsed(t *testing.T) {
	r := newAccessRig(t, okCmds())
	ctx := context.Background()
	got, err := r.access.Hosts(ctx, r.runID)
	if err != nil || len(got) != 1 {
		t.Fatalf("Hosts = %+v, %v", got, err)
	}
	if h := got[0]; h.Name != "nas" || h.Notes != "the NAS" || h.DockerPattern != "remedy-*" || !reflect.DeepEqual(h.Units, []string{"node_exporter.service"}) || h.SudoNone || h.ChangedAt.IsZero() {
		t.Fatalf("host info = %+v", h)
	}

	fields := store.HostFields{Name: "nas", Address: r.srv.Host, Port: r.srv.Port, Account: "remedy", Notes: "the NAS", DockerPattern: "remedy-*", Enabled: true}
	for name, break_ := range map[string]struct {
		do   func() error
		undo func() error
	}{
		"a disabled host": {
			func() error {
				f := fields
				f.Enabled = false
				_, _, err := r.st.UpdateHost(ctx, r.host.ID, f)
				return err
			},
			func() error { _, _, err := r.st.UpdateHost(ctx, r.host.ID, fields); return err },
		},
		"a host without a pinned key": {
			func() error { return r.st.UnpinHostKey(ctx, r.host.ID) },
			func() error { return r.st.PinHostKey(ctx, r.host.ID, r.pin, time.Now()) },
		},
		"a host without a login": {
			func() error { return r.st.DeleteHostCredential(ctx, r.host.ID) },
			func() error { r.setPassword(t, "pw"); return nil },
		},
	} {
		if err := break_.do(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got, err := r.access.Hosts(ctx, r.runID); err != nil || len(got) != 0 {
			t.Errorf("%s: Hosts = %+v, %v, want none", name, got, err)
		}
		if err := break_.undo(); err != nil {
			t.Fatalf("%s (undo): %v", name, err)
		}
		if got, _ := r.access.Hosts(ctx, r.runID); len(got) != 1 {
			t.Fatalf("%s: the host is not usable again after the undo: %+v", name, got)
		}
	}
	if err := r.st.DeleteHost(ctx, r.host.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := r.access.Hosts(ctx, r.runID); err != nil || len(got) != 0 {
		t.Fatalf("a deleted host: Hosts = %+v, %v", got, err)
	}
}

func TestHostsReadsWhetherSudoWorksFromTheLastReport(t *testing.T) {
	r := newAccessRig(t, okCmds())
	ctx := context.Background()
	for report, want := range map[string]bool{`{"sudo":"none"}`: true, `{"sudo":"limited"}`: false, `{"sudo":"unrestricted"}`: false, `not json`: false, ``: false} {
		if err := r.st.SaveHostTest(ctx, r.host.ID, r.pin, store.HostOK, "", report, time.Now()); err != nil {
			t.Fatal(err)
		}
		got, _ := r.access.Hosts(ctx, r.runID)
		if len(got) != 1 || got[0].SudoNone != want {
			t.Errorf("report %q: SudoNone = %+v, want %v", report, got, want)
		}
	}
}

func TestExecSendsTheCanaryAndThenTheQuotedCommandLine(t *testing.T) {
	cmds := okCmds()
	cmds["sudo -n -- 'docker' 'ps' '-a'"] = sshtest.Reply{Out: "CONTAINER\n", Stderr: "warn\n", Exit: 2}
	cmds[`'df' '-h'`] = sshtest.Reply{Out: "Filesystem\n"}
	r := newAccessRig(t, cmds)
	ctx := context.Background()

	res, err := r.access.Exec(ctx, r.runID, "NAS", true, []string{"docker", "ps", "-a"})
	if err != nil || res.Stdout != "CONTAINER\n" || res.Stderr != "warn\n" || res.ExitCode != 2 {
		t.Fatalf("Exec = %+v, %v", res, err)
	}
	if _, err := r.access.Exec(ctx, r.runID, "nas", false, []string{"df", "-h"}); err != nil {
		t.Fatal(err)
	}
	want := []string{hostexec.CanaryCommand(), "sudo -n -- 'docker' 'ps' '-a'", hostexec.CanaryCommand(), `'df' '-h'`}
	if seen := r.srv.Seen(); !reflect.DeepEqual(seen, want) {
		t.Fatalf("the server received\n%q\nwant\n%q", seen, want)
	}
}

func TestExecFailuresAreFixedSentencesThatNameNothingOfTheHost(t *testing.T) {
	ctx := context.Background()
	check := func(name string, err error, want string) {
		t.Helper()
		var arg gatekeeper.ArgumentError
		if !errors.As(err, &arg) || !strings.Contains(arg.Error(), want) {
			t.Errorf("%s: err = %v, want an ArgumentError that says %q", name, err, want)
			return
		}
		for _, leak := range []string{"127.0.0.1", "remedy", "pw", "tcp", "ssh:"} {
			if strings.Contains(arg.Error(), leak) {
				t.Errorf("%s: %q reached the agent in %q", name, leak, arg.Error())
			}
		}
	}

	r := newAccessRig(t, okCmds())
	_, err := r.access.Exec(ctx, r.runID, "other", false, []string{"uptime"})
	check("an unknown host", err, "no host")

	r.setPassword(t, "wrong")
	_, err = r.access.Exec(ctx, r.runID, "nas", false, []string{"uptime"})
	check("a wrong password", err, "rejected the login")

	r.setPassword(t, "pw")
	other := &app.HostAccess{Store: r.st, Key: masterKey(t, 9), SSH: &sshhost.Service{}}
	_, err = other.Exec(ctx, r.runID, "nas", false, []string{"uptime"})
	check("another master key", err, "cannot be decrypted")

	before := r.srv.Attempts() // the wrong password above was an attempt
	r.srv.SetHostSigner(sshtest.NewSigner(t))
	_, err = r.access.Exec(ctx, r.runID, "nas", false, []string{"uptime"})
	check("a changed host key", err, "another key")
	if r.srv.Attempts() != before {
		t.Fatal("a login was offered to a host that showed another key")
	}

	bad := newAccessRig(t, map[string]sshtest.Reply{hostexec.CanaryCommand(): {Out: "not the canary"}})
	_, err = bad.access.Exec(ctx, bad.runID, "nas", false, []string{"uptime"})
	check("a shell that fails the canary", err, "shell is not supported")
	check("a shell that fails the canary", err, "startup file")
	if seen := bad.srv.Seen(); len(seen) != 1 {
		t.Fatalf("the server received %q: only the canary may be sent", seen)
	}

	gone := newAccessRig(t, okCmds())
	gone.srv.Close()
	_, err = gone.access.Exec(ctx, gone.runID, "nas", false, []string{"uptime"})
	check("a closed port", err, "refused")

	cut := newAccessRig(t, map[string]sshtest.Reply{hostexec.CanaryCommand(): {Drop: true}})
	_, err = cut.access.Exec(ctx, cut.runID, "nas", false, []string{"uptime"})
	check("a connection that breaks during the canary", err, "cannot be reached")
}

// A command that was sent and whose end the control plane did not see has an unknown outcome: the host tools need to
// tell it apart from a connection that never got to run anything.
func TestExecAnswersErrHostTimeoutWhenTheOutcomeOfTheCommandIsUnknown(t *testing.T) {
	ctx := context.Background()

	slow := okCmds()
	slow[`'sleep' '5'`] = sshtest.Reply{Out: "late", Delay: 2 * time.Second}
	r := newAccessRig(t, slow)
	r.access.CommandTimeout = 200 * time.Millisecond
	_, err := r.access.Exec(ctx, r.runID, "nas", false, []string{"sleep", "5"})
	if !errors.Is(err, gatekeeper.ErrHostTimeout) {
		t.Errorf("a command that does not end in time: err = %v, want ErrHostTimeout", err)
	}

	broken := okCmds()
	broken[`'reboot'`] = sshtest.Reply{Drop: true}
	r = newAccessRig(t, broken)
	_, err = r.access.Exec(ctx, r.runID, "nas", false, []string{"reboot"})
	if !errors.Is(err, gatekeeper.ErrHostTimeout) {
		t.Errorf("a connection that breaks after the command started: err = %v, want ErrHostTimeout", err)
	}
	if seen := r.srv.Seen(); len(seen) != 2 {
		t.Errorf("the server received %q, want the canary and the command", seen)
	}
}

// A timeout of the canary happens before the command is sent: the agent must not be told that a change may have happened.
func TestExecAfterATimeoutOfTheCanaryIsNotAnUnknownOutcome(t *testing.T) {
	r := newAccessRig(t, map[string]sshtest.Reply{hostexec.CanaryCommand(): {Out: hostexec.CanaryWant(), Delay: 2 * time.Second}})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := r.access.Exec(ctx, r.runID, "nas", false, []string{"reboot"})
	var arg gatekeeper.ArgumentError
	if errors.Is(err, gatekeeper.ErrHostTimeout) || !errors.As(err, &arg) || !strings.Contains(arg.Error(), "cannot be reached") {
		t.Fatalf("err = %v, want the sentence for a host that cannot be reached", err)
	}
	if seen := r.srv.Seen(); len(seen) != 1 {
		t.Fatalf("the server received %q: only the canary may be sent", seen)
	}
}

func TestPickHostPrefersTheExactNameAndRefusesWhatIsNotOneHost(t *testing.T) {
	entry := func(name string, enabled bool) store.HostEntry {
		return store.HostEntry{
			Host:       store.Host{HostFields: store.HostFields{Name: name, Enabled: enabled}, PinnedKey: "k"},
			Credential: &store.HostCredential{},
		}
	}
	pick := func(name string, entries ...store.HostEntry) (string, error) {
		e, err := app.PickHost(entries, name)
		if err != nil {
			return "", err
		}
		return e.Host.Name, nil
	}

	if got, err := pick("nas", entry("NAS", true), entry("nas", true)); err != nil || got != "nas" {
		t.Errorf("an exact match among two that differ in case: %q, %v, want nas", got, err)
	}
	if got, err := pick("nas", entry("Nas", true), entry("other", true)); err != nil || got != "Nas" {
		t.Errorf("one match that differs in case: %q, %v, want Nas", got, err)
	}
	if got, err := pick("nas", entry("Nas", false), entry("NAS", true)); err != nil || got != "NAS" {
		t.Errorf("a host that cannot be used does not count: %q, %v, want NAS", got, err)
	}
	var arg gatekeeper.ArgumentError
	if _, err := pick("nas", entry("other", true)); !errors.As(err, &arg) || !strings.Contains(arg.Error(), "no host") {
		t.Errorf("no match: err = %v, want the sentence for an unknown host", err)
	}
	if _, err := pick("nas", entry("Nas", true), entry("NAS", true)); !errors.As(err, &arg) || !strings.Contains(arg.Error(), "ambiguous") {
		t.Errorf("two matches that differ in case: err = %v, want the sentence for an ambiguous name", err)
	}
}

// sealBlob stores a login whose plaintext is blob, sealed the way the control plane seals it.
func (r *accessRig) sealBlob(t *testing.T, kind string, blob []byte) {
	t.Helper()
	sealed, err := r.key.Seal(blob, store.HostCredentialAAD(r.host.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.st.SaveHostCredential(context.Background(), store.HostCredential{HostID: r.host.ID, Kind: kind, Ciphertext: sealed, Hint: "set"}); err != nil {
		t.Fatal(err)
	}
}

func TestExecWithALoginOrPinThatCannotBeUsedIsAFixedSentence(t *testing.T) {
	ctx := context.Background()
	r := newAccessRig(t, okCmds())
	secretText := "s3cr3t-marker"
	check := func(name string, err error, want string) {
		t.Helper()
		var arg gatekeeper.ArgumentError
		if !errors.As(err, &arg) || !strings.Contains(arg.Error(), want) {
			t.Errorf("%s: err = %v, want an ArgumentError that says %q", name, err, want)
			return
		}
		for _, leak := range []string{"127.0.0.1", "remedy", "pw", secretText, "garbage", "json", "ssh:", "BEGIN"} {
			if strings.Contains(arg.Error(), leak) {
				t.Errorf("%s: %q reached the agent in %q", name, leak, arg.Error())
			}
		}
	}
	exec := func() error { _, err := r.access.Exec(ctx, r.runID, "nas", false, []string{"uptime"}); return err }

	// A blob that was changed after sealing.
	cred, _ := sshhost.NewPasswordCredential("pw")
	blob, _ := cred.Marshal()
	sealed, err := r.key.Seal(blob, store.HostCredentialAAD(r.host.ID))
	if err != nil {
		t.Fatal(err)
	}
	sealed[len(sealed)-1] ^= 0xff
	if err := r.st.SaveHostCredential(ctx, store.HostCredential{HostID: r.host.ID, Kind: "password", Ciphertext: sealed, Hint: "set"}); err != nil {
		t.Fatal(err)
	}
	check("a corrupted sealed blob", exec(), "cannot be decrypted")

	// A blob that opens and is not a login.
	r.sealBlob(t, "password", []byte("not json "+secretText))
	check("a blob that is not a login", exec(), "cannot be used")

	// A key login whose key does not parse.
	bad, _ := sshhost.Credential{Kind: sshhost.KindKey, Key: secret.NewValue("garbage " + secretText)}.Marshal()
	r.sealBlob(t, "key", bad)
	check("a key that does not parse", exec(), "not set up for use")
	if len(r.srv.Seen()) != 0 || r.srv.Attempts() != 0 {
		t.Fatal("something was sent to the host with a login that cannot be used")
	}

	// A pinned key that is not a key.
	r.setPassword(t, "pw")
	if err := r.st.PinHostKey(ctx, r.host.ID, "garbage "+secretText, time.Now()); err != nil {
		t.Fatal(err)
	}
	check("a pin that is not a key", exec(), "not set up for use")
	if r.srv.Attempts() != 0 {
		t.Fatal("a login was offered with a pin that is not a key")
	}
}

// The failure of a command is logged for the maintainer at debug level: its category and the host's name, and nothing the
// library or the host said (an address, an account, a credential, a port).
func TestExecLogsOnlyTheCategoryOfAFailure(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	r := newAccessRig(t, okCmds())
	r.access.Log = log
	r.setPassword(t, "wrong-secret-pw")
	_, _ = r.access.Exec(ctx, r.runID, "nas", false, []string{"uptime"})
	r.srv.SetHostSigner(sshtest.NewSigner(t))
	r.setPassword(t, "pw")
	_, _ = r.access.Exec(ctx, r.runID, "nas", false, []string{"uptime"})

	bad := newAccessRig(t, map[string]sshtest.Reply{hostexec.CanaryCommand(): {Out: "not the canary"}})
	bad.access.Log = log
	_, _ = bad.access.Exec(ctx, bad.runID, "nas", false, []string{"uptime"})

	gone := newAccessRig(t, okCmds())
	gone.access.Log = log
	gone.srv.Close()
	_, _ = gone.access.Exec(ctx, gone.runID, "nas", false, []string{"uptime"})

	out := buf.String()
	for _, want := range []string{"category=auth", "category=hostkey", "category=shell", "category=refused", "host=nas"} {
		if !strings.Contains(out, want) {
			t.Errorf("the log lacks %q:\n%s", want, out)
		}
	}
	for _, leak := range []string{"127.0.0.1", "wrong-secret-pw", "pw", "remedy", "tcp", "ssh:", "unable to authenticate", "dial", r.srv.Host, strconv.Itoa(r.srv.Port), strconv.Itoa(gone.srv.Port)} {
		if strings.Contains(out, leak) {
			t.Errorf("%q is in the log:\n%s", leak, out)
		}
	}
}
