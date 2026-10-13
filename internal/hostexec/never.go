package hostexec

import "strings"

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

// hasFlag says whether the words contain one of the long flags, or a short-flag cluster with one of the letters (-rf has r).
func hasFlag(words []string, letters string, longs ...string) bool {
	for _, w := range words {
		if in(w, longs...) {
			return true
		}
		if strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "--") && strings.ContainsAny(w[1:], letters) {
			return true
		}
	}
	return false
}

// touchesSystem says whether a path among the words is / or lies in a system directory.
func touchesSystem(words []string) bool {
	for _, w := range words {
		if strings.HasPrefix(w, "-") {
			continue
		}
		if w == "/" || w == "/*" {
			return true
		}
		for _, d := range systemDirs {
			if w == d || strings.HasPrefix(w, d+"/") {
				return true
			}
		}
	}
	return false
}

// NeverReason says why a command is never run, or "" when the list has nothing against it. The list is in code, applies to
// `host_change` before an approval is asked for and again when the call runs, and holds also when the maintainer would approve.
//
// It looks at EVERY word as a possible program (with the words after it as its arguments), so a program behind sudo, env,
// nohup, timeout or xargs is seen as well. The price is a false positive for a container or unit named like a dangerous
// program; that direction is safe. A reason is one short sentence without a final period.
func NeverReason(argv []string) string {
	for _, w := range argv {
		for _, p := range protectedNames {
			if strings.Contains(w, p) {
				return "it names " + p + ", a file that grants access, which Remedy never touches"
			}
		}
	}
	for i, w := range argv {
		if why := programReason(base(w), argv[i+1:]); why != "" {
			return why
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
			if strings.HasPrefix(w, "of=/dev/") {
				return "it writes onto a device"
			}
		}
	case prog == "rm":
		if hasWord(rest, "--no-preserve-root") || (hasFlag(rest, "rR", "--recursive") && touchesSystem(rest)) {
			return "it removes a system path recursively"
		}
	case in(prog, "chmod", "chown", "chgrp"):
		if hasFlag(rest, "R", "--recursive") && touchesSystem(rest) {
			return "it changes the permissions of a system path recursively"
		}
	case in(prog, "sh", "bash", "dash", "zsh", "ksh", "fish", "csh", "tcsh", "ash", "mksh"):
		return "it starts a shell, which would run code that cannot be shown or judged"
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
