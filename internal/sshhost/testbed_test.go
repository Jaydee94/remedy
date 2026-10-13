package sshhost_test

import (
	"os"
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
	// The sudoers file is only read by sudo when its mode is 0440 and visudo accepts it.
	if !strings.Contains(df, "chmod 0440 /etc/sudoers.d/remedy") {
		t.Error("the sudoers file must be mode 0440")
	}
}
