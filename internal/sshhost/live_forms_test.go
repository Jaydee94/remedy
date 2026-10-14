package sshhost_test

// liveRead is one read of the table of hostexec as the live tests run it, with a text that the testbed's output must contain.
type liveRead struct {
	Argv []string
	Want string
}

// liveReads has one command for every form of hostexec.ReadForms(); TestTheLiveTestsCoverEveryFormOfTheTable fails when a form is
// added to the table and not here. It has no build tag so that the check runs without Docker.
var liveReads = []liveRead{
	{[]string{"docker", "ps", "-a"}, "remedy-drill"},
	{[]string{"docker", "logs", "--tail", "5", "remedy-drill"}, "log line 1 of remedy-drill"},
	{[]string{"docker", "inspect", "remedy-drill"}, `"Running":true`},
	{[]string{"systemctl", "status", "node_exporter.service", "--no-pager"}, "active"},
	{[]string{"systemctl", "is-active", "node_exporter.service"}, "active"},
	{[]string{"systemctl", "list-units", "--failed", "--no-pager"}, "0 loaded units listed."},
	{[]string{"journalctl", "-u", "node_exporter.service", "-n", "5", "--no-pager"}, "node_exporter.service"},
	{[]string{"df", "-h"}, "Filesystem"},
	{[]string{"free", "-m"}, "Mem:"},
	{[]string{"uptime"}, "load average"},
	{[]string{"uname", "-a"}, "Linux"},
	{[]string{"ps", "-eo", "pid,user,pcpu,pmem,etime,comm"}, "sshd"},
	{[]string{"ss", "-tln"}, "LISTEN"},
}

// LiveReadArgv is the commands of liveReads.
func LiveReadArgv() [][]string {
	out := make([][]string, len(liveReads))
	for i, r := range liveReads {
		out[i] = r.Argv
	}
	return out
}
