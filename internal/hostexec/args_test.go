package hostexec_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/hostexec"
)

func TestValidateArgv(t *testing.T) {
	ok := [][]string{
		{"uptime"},
		{"docker", "logs", "--tail", "50", "remedy-drill"},
		{"echo", "a b", "it's", "$(id)", "日本語"},
		{strings.Repeat("x", hostexec.MaxWordBytes)},
	}
	for _, argv := range ok {
		if err := hostexec.ValidateArgv(argv); err != nil {
			t.Errorf("ValidateArgv(%q) = %v", argv, err)
		}
	}

	long := make([]string, hostexec.MaxWords+1)
	for i := range long {
		long[i] = "x"
	}
	many := make([]string, 5)
	for i := range many {
		many[i] = strings.Repeat("y", 1000)
	}
	bad := map[string][]string{
		"no words":          {},
		"nil":               nil,
		"an empty word":     {"ls", ""},
		"a newline":         {"ls", "a\nb"},
		"a carriage return": {"ls", "a\rb"},
		"a tab":             {"ls", "a\tb"},
		"a NUL":             {"ls", "a\x00b"},
		"an escape":         {"ls", "\x1b[31m"},
		"a DEL":             {"ls", "a\x7fb"},
		"invalid UTF-8":     {"ls", "\xff\xfe"},
		"a C1 control":      {"ls", "a\u0085b"},
		"a word too long":   {strings.Repeat("x", hostexec.MaxWordBytes+1)},
		"too many words":    long,
		"too many bytes":    many,
	}
	for name, argv := range bad {
		err := hostexec.ValidateArgv(argv)
		var ae hostexec.ArgError
		if !errors.As(err, &ae) || ae == "" {
			t.Errorf("%s: err = %v, want an ArgError with a sentence", name, err)
		}
	}
}

func TestValidateProgram(t *testing.T) {
	for _, argv := range [][]string{
		{"docker", "restart", "x"},
		{"/usr/bin/docker", "restart", "x"},
		{"systemctl"},
		{"g++"},
		{"python3.12", "script.py"},
	} {
		if err := hostexec.ValidateProgram(argv); err != nil {
			t.Errorf("ValidateProgram(%q) = %v", argv, err)
		}
	}
	for name, argv := range map[string][]string{
		"an option of sudo":       {"-s"},
		"a long option":           {"--shell"},
		"an environment variable": {"FOO=bar", "docker"},
		"a relative path":         {"./x"},
		"a parent path":           {"../x"},
		"a dot":                   {".hidden"},
		"a trailing slash":        {"/usr/bin/"},
		"a double slash":          {"//usr/bin/x"},
		"a space":                 {"my program"},
	} {
		if err := hostexec.ValidateProgram(argv); err == nil {
			t.Errorf("%s: ValidateProgram(%q) = nil, want an error", name, argv)
		}
	}
}
