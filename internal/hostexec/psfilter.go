package hostexec

import "strings"

// psForm is the form of the table that lists the containers. Expand compares a verdict with it, so the table and the
// expansion cannot name two different commands.
const psForm = "docker ps -a"

// PsFilter is the argument of --filter that limits `docker ps -a` to the containers a host's pattern allows: the pattern
// `remedy-*` becomes `name=^remedy-` and an exact name `remedy-drill` becomes `name=^remedy-drill$`. The sudoers line the
// onboarding snippet writes names the same text (a test in this package pins both), so sudo refuses any other filter. The
// pattern is not escaped: a dot of it is a dot of the regular expression, which is looser than the pattern says, and the
// spec accepts that. A pattern that is empty or does not pass the check of the onboarding has no filter, and the command
// must then not run at all.
func PsFilter(pattern string) (string, bool) {
	if pattern == "" || !patternRE.MatchString(pattern) {
		return "", false
	}
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
		return "name=^" + prefix, true
	}
	return "name=^" + pattern + "$", true
}

// Expand is the command that runs on the host for a classified command. Every verdict but a read of `docker ps -a` runs
// as it was asked. That one runs with the host's filter, so that a host shows only the containers of its pattern (a Paperless
// container next to the drill container never reaches the agent). It is false when no filter can be built, and the
// plain `docker ps -a` is never the fallback.
func Expand(v Verdict, argv []string, r Rules) ([]string, bool) {
	if v.Form != psForm {
		return argv, true
	}
	f, ok := PsFilter(r.DockerPattern)
	if !ok {
		return nil, false
	}
	return append(append([]string(nil), argv...), "--filter", f), true
}
