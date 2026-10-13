package hostexec

import (
	"path"
	"strings"
	"unicode"
)

// protectedNames are files that grant access. A command that has one of them among its words is refused.
var protectedNames = []string{"authorized_keys", "sudoers", "/etc/passwd", "/etc/shadow", "sshd_config"}

// systemDirs are the paths a recursive removal or permission change is never run on.
var systemDirs = []string{"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/var", "/home", "/root", "/opt", "/srv", "/dev", "/proc", "/sys"}

func base(word string) string {
	if i := strings.LastIndex(word, "/"); i >= 0 {
		return word[i+1:]
	}
	return word
}

func in(s string, set ...string) bool {
	for _, x := range set {
		if s == x {
			return true
		}
	}
	return false
}

func hasWord(words []string, set ...string) bool {
	for _, w := range words {
		if in(w, set...) {
			return true
		}
	}
	return false
}

// longMatch says whether a word is one of the long options, or a prefix of one that getopt would accept (--rec for
// --recursive, --out=/x for --output). At least two letters after the dashes, so -- and --r match nothing.
func longMatch(w string, longs []string) bool {
	if !strings.HasPrefix(w, "--") {
		return false
	}
	name := w[2:]
	if i := strings.IndexByte(name, '='); i >= 0 {
		name = name[:i]
	}
	if len(name) < 2 {
		return false
	}
	for _, l := range longs {
		if strings.HasPrefix(l[2:], name) {
			return true
		}
	}
	return false
}

// hasLong says whether one of the words is one of the long options (or an unambiguous-enough prefix of one).
func hasLong(words []string, longs ...string) bool {
	for _, w := range words {
		if longMatch(w, longs) {
			return true
		}
	}
	return false
}

// hasFlag says whether the words contain one of the long flags, or a short-flag cluster with one of the letters (-rf has r).
func hasFlag(words []string, letters string, longs ...string) bool {
	for _, w := range words {
		if longMatch(w, longs) {
			return true
		}
		if strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "--") && strings.ContainsAny(w[1:], letters) {
			return true
		}
	}
	return false
}

// forms returns a word as it was given and, for an absolute path, as path.Clean makes it (// runs, ./ and ../ resolved), so
// that /tmp/../etc and /etc//shadow are seen as the paths they name. Both forms are checked.
func forms(word string) []string {
	if !strings.HasPrefix(word, "/") {
		return []string{word}
	}
	if c := path.Clean(word); c != word {
		return []string{word, c}
	}
	return []string{word}
}

// touchesSystem says whether a path among the words is / or lies in a system directory.
func touchesSystem(words []string) bool {
	for _, w := range words {
		if strings.HasPrefix(w, "-") {
			continue
		}
		for _, f := range forms(w) {
			if f == "/" || f == "/*" {
				return true
			}
			for _, d := range systemDirs {
				if f == d || strings.HasPrefix(f, d+"/") {
					return true
				}
			}
		}
	}
	return false
}

// shellSeparator says whether a rune ends a token of a shell line: white space, or a character a shell reads as an operator,
// a quote, an expansion or an escape.
func shellSeparator(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune(";&|()<>`$'\"\\", r)
}

// expand returns the words of argv and, for a word that a shell would split (white space, ; & | ( ) < > ` $ ' " \), its
// tokens after it. A shell string handed to su -c or env -S is a command line, so each of its tokens is seen as a possible
// program as well.
func expand(argv []string) []string {
	out := append([]string(nil), argv...)
	for _, w := range argv {
		if f := strings.FieldsFunc(w, shellSeparator); len(f) != 1 || f[0] != w {
			out = append(out, f...)
		}
	}
	return out
}

// NeverReason says why a command is never run, or "" when the list has nothing against it. The list is in code, applies to
// `host_change` before an approval is asked for and again when the call runs, and holds also when the maintainer would approve.
//
// It looks at EVERY word as a possible program (with the words after it as its arguments), and at every whitespace token of a
// word, so a program behind sudo, env, nohup, timeout, xargs or inside a shell string is seen as well. Paths are checked as
// given and as cleaned. The price is a false positive for a container or unit named like a dangerous program; that direction
// is safe. A reason is one short sentence without a final period.
func NeverReason(argv []string) string {
	words := expand(argv)
	for _, w := range words {
		for _, f := range forms(w) {
			for _, p := range protectedNames {
				if strings.Contains(f, p) {
					return "it names " + p + ", a file that grants access, which Remedy never touches"
				}
			}
		}
	}
	for i, w := range words {
		for _, f := range forms(w) {
			if why := programReason(base(f), words[i+1:]); why != "" {
				return why
			}
		}
	}
	return ""
}

func programReason(prog string, rest []string) string {
	switch {
	case strings.HasPrefix(prog, "mkfs"):
		return "it creates a filesystem"
	case in(prog, "mke2fs", "wipefs", "fdisk", "sfdisk", "cfdisk", "parted", "sgdisk", "gdisk", "shred"):
		return "it changes or destroys a disk"
	case prog == "dd":
		for _, w := range rest {
			if strings.HasPrefix(w, "of=") && touchesDevice(strings.TrimPrefix(w, "of=")) {
				return "it writes onto a device"
			}
		}
	case prog == "rm":
		if hasLong(rest, "--no-preserve-root") || (hasFlag(rest, "rR", "--recursive") && touchesSystem(rest)) {
			return "it removes a system path recursively"
		}
	case in(prog, "chmod", "chown", "chgrp"):
		if hasFlag(rest, "R", "--recursive") && touchesSystem(rest) {
			return "it changes the permissions of a system path recursively"
		}
	case in(prog, "sh", "bash", "dash", "zsh", "ksh", "fish", "csh", "tcsh", "ash", "mksh"):
		return "it starts a shell, which would run code that cannot be shown or judged"
	case prog == "su":
		return "su starts a shell as another user, and its -c runs a string as a shell line; sudo -n is the only privilege path"
	case prog == "doas":
		return "doas runs commands as another user; sudo -n is the only privilege path"
	case prog == "env":
		if hasFlag(rest, "S", "--split-string") {
			return "env -S runs a string as a command line, which cannot be shown or judged"
		}
	case in(prog, "nsenter", "chroot"):
		return "it enters another namespace or root, which is a shell by another name"
	case prog == "sudo":
		if hasFlag(rest, "sie", "--shell", "--login", "--edit") {
			return "sudo -s, -i or -e gives a root shell or an editor that can run commands"
		}
	case strings.HasPrefix(prog, "python") || in(prog, "perl", "ruby", "node", "nodejs", "php", "lua", "luajit", "tclsh"):
		if hasFlag(rest, "cerE", "--eval", "--command", "--exec", "--print") {
			return "it runs code given as a string"
		}
	case in(prog, "awk", "gawk", "mawk", "nawk"):
		return "awk can run commands (system()) that cannot be shown or judged"
	case prog == "eval":
		return "it runs a string as code"
	case prog == "curl":
		if hasFlag(rest, "oOT", "--output", "--remote-name", "--upload-file") {
			return "it downloads or uploads files"
		}
	case prog == "wget":
		if hasFlag(rest, "O", "--output-document", "--post-file") {
			return "it downloads or uploads files"
		}
	case in(prog, "shutdown", "reboot", "poweroff", "halt"):
		return "it powers off or reboots the machine"
	case in(prog, "init", "telinit"):
		if hasWord(rest, "0", "6") {
			return "it powers off or reboots the machine"
		}
	case prog == "systemctl":
		if hasWord(rest, "poweroff", "reboot", "halt", "kexec", "suspend", "hibernate", "hybrid-sleep") {
			return "it powers off or reboots the machine"
		}
		if hasWord(rest, "stop", "disable", "mask") && hasWord(rest, "ssh", "sshd", "ssh.service", "sshd.service", "ssh.socket") {
			return "it would cut off the SSH access that Remedy and the maintainer use"
		}
	case in(prog, "iptables", "ip6tables", "nft", "ufw", "firewall-cmd"):
		return "it changes the firewall, which could lock everyone out or open the host"
	case in(prog, "ssh", "scp", "sftp", "rsync", "nc", "ncat", "netcat", "socat", "telnet"):
		// The words after the program are its arguments; a bare word such as ssh in systemctl restart ssh is a name.
		if len(rest) > 0 {
			return "it opens a connection to another machine"
		}
	case prog == "find":
		if hasWord(rest, "-exec", "-execdir", "-ok", "-okdir") {
			return "find -exec runs commands that cannot be shown or judged"
		}
	case prog == "xargs":
		return "xargs builds and runs commands that cannot be shown or judged"
	case prog == "docker":
		if hasWord(rest, "run", "exec", "cp", "build", "commit", "import", "load", "save", "plugin", "swarm", "create") {
			return "this docker command can run code or move files outside the container"
		}
	case in(prog, "passwd", "chpasswd", "chsh", "usermod", "useradd", "userdel", "adduser", "deluser", "visudo"):
		return "it changes accounts or their access"
	}
	return ""
}

// touchesDevice says whether a dd target is a device file, as given or cleaned (//dev/sda names /dev/sda).
func touchesDevice(target string) bool {
	for _, f := range forms(target) {
		if strings.HasPrefix(f, "/dev/") {
			return true
		}
	}
	return false
}
