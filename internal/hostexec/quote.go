// Package hostexec decides what an agent may run on a host and builds the command line that is sent. It does no I/O:
// everything in it is a function of its arguments, so that the one barrier Remedy owns on a host can be tested hard.
package hostexec

import (
	"regexp"
	"strings"
)

// Quote wraps a word in single quotes for a POSIX shell. Inside single quotes nothing is special; a single quote itself
// is closed, escaped and opened again.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// CommandLine is the one line OpenSSH hands to the account's shell: every word quoted, joined by spaces. With sudo it
// starts with `sudo -n --`: never a password prompt, and no word after it is read as an option of sudo.
func CommandLine(argv []string, sudo bool) string {
	words := make([]string, len(argv))
	for i, w := range argv {
		words[i] = Quote(w)
	}
	line := strings.Join(words, " ")
	if sudo {
		return "sudo -n -- " + line
	}
	return line
}

var plainWord = regexp.MustCompile(`^[A-Za-z0-9@%+=:,./_-]+$`)

// Display is the argv as a person would type it: a word is quoted only when it needs to be. It is what the maintainer
// reads on the approval card and in the activity log. web/src/hostcmd.ts does the same; a test pins both to the same cases.
func Display(argv []string) string {
	words := make([]string, len(argv))
	for i, w := range argv {
		if plainWord.MatchString(w) {
			words[i] = w
		} else {
			words[i] = Quote(w)
		}
	}
	return strings.Join(words, " ")
}

// canaryText has every character a shell treats as special, but no control character (arguments may not have one).
const canaryText = "a b'c\"d$(e)`f`;g|h&i>j<k*l?m\\n~#{}!"

// CanaryCommand is run first in every connection. The account's shell must print the canary exactly: a shell that does not
// (fish, csh, a restricted shell) does not read quoted words the way the rest of this package assumes.
func CanaryCommand() string { return "printf %s " + Quote(canaryText) }

// CanaryWant is what CanaryCommand must print.
func CanaryWant() string { return canaryText }
