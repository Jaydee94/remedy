package sshhost_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/hostexec"
	"github.com/Jaydee94/remedy/internal/sshhost"
)

// The testbed proves that the lines the onboarding snippet writes do what they should, so the file of the testbed must BE the
// snippet's output for its rules. When the snippet changes, regenerate dev/ssh/sudoers-remedy (the test prints the new text).
func TestTheTestbedSudoersFileIsWhatTheSnippetWrites(t *testing.T) {
	out, err := sshhost.Snippet(sshhost.SnippetInput{Account: "remedy", DockerPattern: "remedy-*", SystemdUnits: []string{"node_exporter.service"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../dev/ssh/sudoers-remedy")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != out.Sudoers {
		t.Fatalf("dev/ssh/sudoers-remedy differs from the snippet; it must be exactly:\n%s", out.Sudoers)
	}
}

// Every form of the table of reads that runs through sudo must have a line in the sudoers file of the testbed, so that the
// table and the snippet cannot drift apart without a test noticing (the live tests then run each form through a real sudo).
func TestEveryReadFormThatNeedsSudoIsGrantedByTheTestbedSudoers(t *testing.T) {
	sudoers, err := os.ReadFile("../../dev/ssh/sudoers-remedy")
	if err != nil {
		t.Fatal(err)
	}
	rules := hostexec.Rules{DockerPattern: "remedy-*", Units: []string{"node_exporter.service"}}
	for _, argv := range LiveReadArgv() {
		v := hostexec.Classify(argv, rules)
		if v.Kind != hostexec.Read {
			t.Errorf("%q: %+v, want a read", argv, v)
			continue
		}
		if !v.Sudo {
			continue
		}
		// The snippet has one line per verb; the container name is a pattern there, so only the command and its verb are compared.
		want := "/usr/bin/" + argv[0] + " " + argv[1]
		if !strings.Contains(string(sudoers), want) {
			t.Errorf("%q needs sudo, but the testbed's sudoers file has no line starting with %q", argv, want)
		}
	}
}

// Every read form of the table must be exercised by the live tests: a new form in hostexec.ReadForms() that the live test
// does not know fails here, so nobody adds a form that no real sudo has seen.
func TestTheLiveTestsCoverEveryFormOfTheTable(t *testing.T) {
	rules := hostexec.Rules{DockerPattern: "remedy-*", Units: []string{"node_exporter.service"}}
	covered := map[string]bool{}
	for _, argv := range LiveReadArgv() {
		v := hostexec.Classify(argv, rules)
		if v.Kind == hostexec.Read {
			covered[v.Form] = true
		}
	}
	for _, f := range hostexec.ReadForms() {
		if !covered[f] {
			t.Errorf("the form %q of the table is not run by the live tests (internal/sshhost/live_forms_test.go)", f)
		}
	}
}

// The fakes of the testbed must be the scripts the Dockerfile copies, and the Dockerfile must put the files where sshd and sudo read them.
func TestTheDockerfileInstallsWhatTheTestbedNeeds(t *testing.T) {
	b, err := os.ReadFile("../../dev/ssh/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	df := string(b)
	for _, want := range []string{
		"COPY fakes/docker /usr/bin/docker",
		"COPY fakes/systemctl /usr/bin/systemctl",
		"COPY sudoers-remedy /etc/sudoers.d/remedy",
		"COPY sshd_config.d/remedy.conf /etc/ssh/sshd_config.d/remedy.conf",
		"useradd -m -s /bin/bash",
		"useradd -m -s /usr/bin/fish",
		"visudo -cf /etc/sudoers.d/remedy",
	} {
		if !strings.Contains(df, want) {
			t.Errorf("dev/ssh/Dockerfile lacks %q", want)
		}
	}
	for _, p := range []string{"../../dev/ssh/fakes/docker", "../../dev/ssh/fakes/systemctl", "../../dev/ssh/sshd_config.d/remedy.conf"} {
		if _, err := os.Stat(p); err != nil {
			t.Error(err)
		}
	}
	if !strings.Contains(df, "COPY fakes/journalctl /usr/bin/journalctl") {
		t.Error("dev/ssh/Dockerfile lacks the COPY of fakes/journalctl")
	}
	if _, err := os.Stat("../../dev/ssh/fakes/journalctl"); err != nil {
		t.Error(err)
	}
	// The sudoers file is only read by sudo when its mode is 0440 and visudo accepts it.
	if !strings.Contains(df, "chmod 0440 /etc/sudoers.d/remedy") {
		t.Error("the sudoers file must be mode 0440")
	}
}

// up.sh must never touch a key or a directory that is not its own: it refuses a state directory that is ~/.ssh or holds a file it
// did not make, before it starts Docker (so this test needs no Docker).
func TestUpScriptRefusesAStateDirectoryThatIsNotItsOwn(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	home := t.TempDir()
	cases := map[string]func(t *testing.T) string{
		"the ssh directory of the user": func(t *testing.T) string {
			d := filepath.Join(home, ".ssh")
			if err := os.MkdirAll(d, 0o700); err != nil {
				t.Fatal(err)
			}
			return d
		},
	}
	for _, name := range []string{"known_hosts", "authorized_keys", "id_ed25519", "id_rsa.pub"} {
		cases["a directory with "+name] = func(t *testing.T) string {
			d := t.TempDir()
			if err := os.WriteFile(filepath.Join(d, name), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			return d
		}
	}
	for what, mk := range cases {
		t.Run(what, func(t *testing.T) {
			dir := mk(t)
			cmd := exec.Command("sh", "../../dev/ssh/up.sh")
			cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "REMEDY_SSH_DIR=" + dir}
			out, err := cmd.CombinedOutput()
			var ee *exec.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != 1 {
				t.Fatalf("err = %v, want exit status 1; output:\n%s", err, out)
			}
			if !strings.Contains(string(out), "not made by the testbed") {
				t.Errorf("the refusal does not say why:\n%s", out)
			}
		})
	}
}

// The key of the testbed has a name of its own, env.sh quotes its paths, and a timeout removes the container.
func TestUpScriptMakesItsOwnKeyAndCleansUp(t *testing.T) {
	b, err := os.ReadFile("../../dev/ssh/up.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"remedy_testbed_ed25519",
		"export REMEDY_SSH_KEY_FILE='$out/remedy_testbed_ed25519'",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("dev/ssh/up.sh lacks %q", want)
		}
	}
	if strings.Contains(s, "id_ed25519") {
		t.Error("dev/ssh/up.sh still names id_ed25519")
	}
	i := strings.Index(s, "does not answer")
	if i < 0 {
		t.Fatal("dev/ssh/up.sh has no timeout message")
	}
	rest := s[i:]
	if rm, ex := strings.Index(rest, "docker rm -f"), strings.Index(rest, "exit 1"); rm < 0 || ex < 0 || rm > ex {
		t.Error("dev/ssh/up.sh must remove the container before it exits on a timeout")
	}
}
