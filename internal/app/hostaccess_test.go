package app_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"reflect"
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
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
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
