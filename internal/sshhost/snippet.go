package sshhost

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var authLineRE = regexp.MustCompile(`^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp(256|384|521)) [A-Za-z0-9+/=]+$`)

// SnippetInput is what the onboarding snippet is made from. Every value is checked again here, because the output is a file
// that grants rights.
type SnippetInput struct {
	Account       string
	AuthorizedKey string // the public key line of the login, or empty (a password login needs none)
	DockerPattern string
	SystemdUnits  []string // already normalised: each ends in .service, .timer or .socket
}

type SnippetOutput struct {
	AuthorizedKeys string
	Sudoers        string
}

// Snippet makes the text the maintainer runs on the host. It never runs anything itself. A value that does not pass its check
// is an error, not text.
func Snippet(in SnippetInput) (SnippetOutput, error) {
	if err := checkAccount(in.Account); err != nil {
		return SnippetOutput{}, err
	}
	if err := checkDocker(in.DockerPattern); err != nil {
		return SnippetOutput{}, err
	}
	if len(in.SystemdUnits) > MaxUnits {
		return SnippetOutput{}, errors.New("too many units")
	}
	for _, u := range in.SystemdUnits {
		if !unitRE.MatchString(u) {
			return SnippetOutput{}, fmt.Errorf("the unit %q is not valid", u)
		}
	}
	var out SnippetOutput

	if in.AuthorizedKey != "" {
		if !authLineRE.MatchString(in.AuthorizedKey) {
			return SnippetOutput{}, errors.New("the public key is not a plain key line")
		}
		out.AuthorizedKeys = "# Append to ~" + in.Account + "/.ssh/authorized_keys (mode 600, owned by the account)\n" + in.AuthorizedKey + "\n"
	}

	var b strings.Builder
	b.WriteString("# Save as /etc/sudoers.d/remedy with: sudo visudo -f /etc/sudoers.d/remedy\n")
	b.WriteString("# Read every line. Check the paths first: command -v docker; command -v systemctl\n")
	b.WriteString("# Reading the journal needs the group systemd-journal, not sudo.\n")
	if p := in.DockerPattern; p != "" {
		a := in.Account + " ALL=(root) NOPASSWD: "
		b.WriteString(a + "/usr/bin/docker ps -a\n")
		b.WriteString(a + "/usr/bin/docker logs --tail * " + p + "\n")
		b.WriteString(a + "/usr/bin/docker inspect " + p + "\n")
		b.WriteString(a + "/usr/bin/docker start " + p + ", /usr/bin/docker restart " + p + ", /usr/bin/docker stop " + p + "\n")
	}
	for _, u := range in.SystemdUnits {
		b.WriteString(in.Account + " ALL=(root) NOPASSWD: /usr/bin/systemctl start " + u +
			", /usr/bin/systemctl restart " + u + ", /usr/bin/systemctl stop " + u + "\n")
	}
	out.Sudoers = b.String()
	return out, nil
}
