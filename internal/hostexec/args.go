package hostexec

import (
	"fmt"
	"regexp"
	"unicode"
	"unicode/utf8"
)

const (
	MaxWords     = 64
	MaxWordBytes = 1024
	MaxLineBytes = 4096
)

// ArgError is a problem with the words an agent sent, worded so that it can be shown to the agent as it is.
type ArgError string

func (e ArgError) Error() string { return string(e) }

// ValidateArgv checks the words: how many, how long, valid UTF-8, no control character and no NUL. The first problem is
// returned. Nothing that fails this is ever sent to a host.
func ValidateArgv(argv []string) error {
	if len(argv) == 0 {
		return ArgError("argv needs at least one word: the program")
	}
	if len(argv) > MaxWords {
		return ArgError(fmt.Sprintf("argv has %d words; at most %d are allowed", len(argv), MaxWords))
	}
	total := 0
	for i, w := range argv {
		switch {
		case w == "":
			return ArgError(fmt.Sprintf("word %d is empty", i+1))
		case len(w) > MaxWordBytes:
			return ArgError(fmt.Sprintf("word %d is longer than %d bytes", i+1, MaxWordBytes))
		case !utf8.ValidString(w):
			return ArgError(fmt.Sprintf("word %d is not valid UTF-8", i+1))
		}
		for _, r := range w {
			if unicode.IsControl(r) {
				return ArgError(fmt.Sprintf("word %d has a control character (a newline, a tab, a NUL or an escape); pass such text as a single plain word", i+1))
			}
		}
		total += len(w) + 1
	}
	if total > MaxLineBytes {
		return ArgError(fmt.Sprintf("argv is longer than %d bytes", MaxLineBytes))
	}
	return nil
}

// programName is an absolute path (every part a name, no empty part) or a bare program name; a relative path with a slash
// (bin/x) is neither.
var programName = regexp.MustCompile(`^(/[A-Za-z0-9][A-Za-z0-9._+-]*(/[A-Za-z0-9][A-Za-z0-9._+-]*)*|[A-Za-z0-9][A-Za-z0-9._+-]*)$`)

// ValidateProgram checks the first word of a change: a program name or an absolute path. A leading dash would be an option
// of sudo, a word with = an environment assignment for sudo, a relative path something that depends on the directory.
func ValidateProgram(argv []string) error {
	if len(argv) == 0 || !programName.MatchString(argv[0]) {
		return ArgError("the first word must be a program name such as docker, or an absolute path; it may not start with - and may not contain =")
	}
	return nil
}
