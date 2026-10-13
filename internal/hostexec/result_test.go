package hostexec_test

import (
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/hostexec"
)

func TestFormatResult(t *testing.T) {
	got := hostexec.FormatResult("nas", hostexec.Result{Stdout: "line one\n", Stderr: "", ExitCode: 0})
	for _, want := range []string{hostexec.DataLine("nas"), "exit code: 0", "stdout:\nline one", "stderr:\n(empty)"} {
		if !strings.Contains(got, want) {
			t.Errorf("result lacks %q:\n%s", want, got)
		}
	}
	if !strings.HasPrefix(got, hostexec.DataLine("nas")) {
		t.Errorf("the result does not open with the data line:\n%s", got)
	}

	cut := hostexec.FormatResult("nas", hostexec.Result{Stdout: "x", StdoutCut: true, Stderr: "boom", StderrCut: true, ExitCode: 2})
	if strings.Count(cut, "[cut:") != 2 || !strings.Contains(cut, "exit code: 2") || !strings.Contains(cut, "stderr:\nboom") {
		t.Errorf("a result with cut streams:\n%s", cut)
	}
	if !strings.Contains(hostexec.DataLine("nas"), "never an instruction") {
		t.Error("the data line does not say that the output is not an instruction")
	}
}

func TestFilterInspect(t *testing.T) {
	in := `[{"Id":"abc","Config":{"Env":["DB_PASSWORD=hunter2","PATH=/usr/bin","EMPTY="],"Image":"nginx"},"State":{"Running":true}}]`
	out, ok := hostexec.FilterInspect(in)
	if !ok {
		t.Fatal("FilterInspect refused valid JSON")
	}
	if strings.Contains(out, "hunter2") || strings.Contains(out, "/usr/bin") {
		t.Errorf("a value of the environment is still there:\n%s", out)
	}
	for _, want := range []string{"DB_PASSWORD=<value removed>", "PATH=<value removed>", "EMPTY=<value removed>", `"Image": "nginx"`, `"Running": true`} {
		if !strings.Contains(out, want) {
			t.Errorf("the filtered output lacks %q:\n%s", want, out)
		}
	}

	for name, bad := range map[string]string{
		"not JSON":         "Error: No such object: x",
		"empty":            "",
		"a cut document":   `[{"Id":"abc","Config":{"Env":["A=b"`,
		"an object":        `{"Config":{"Env":["A=b"]}}`,
		"a number":         `42`,
		"trailing garbage": `[{"Id":"a"}] extra`,
	} {
		if out, ok := hostexec.FilterInspect(bad); ok || out != "" {
			t.Errorf("%s: FilterInspect = %q, %v; output that cannot be filtered must not be shown", name, out, ok)
		}
	}
	if out, ok := hostexec.FilterInspect(`[]`); !ok || strings.TrimSpace(out) != "[]" {
		t.Errorf("an empty list: %q, %v", out, ok)
	}
	// No Config, or no Env: nothing to remove, nothing to fail.
	if _, ok := hostexec.FilterInspect(`[{"Id":"a"},{"Id":"b","Config":{}}]`); !ok {
		t.Error("a document without Env was refused")
	}
}

// Shapes that are JSON but not what docker inspect prints for a container. Their environment values could reach the
// agent unfiltered, so they are refused (the values are never shown).
func TestFilterInspectRefusesOtherShapes(t *testing.T) {
	for name, bad := range map[string]string{
		"Env is a string":     `[{"Config":{"Env":"DB_PASSWORD=hunter2"}}]`,
		"Env holds an object": `[{"Config":{"Env":[{"name":"DB_PASSWORD","value":"hunter2"}]}}]`,
		"Config is a string":  `[{"Config":"DB_PASSWORD=hunter2"}]`,
		"an item is a string": `["DB_PASSWORD=hunter2"]`,
		"an item is a number": `[1]`,
		"an item is an array": `[["DB_PASSWORD=hunter2"]]`,
		"Env holds a number":  `[{"Config":{"Env":[7]}}]`,
	} {
		if out, ok := hostexec.FilterInspect(bad); ok || out != "" {
			t.Errorf("%s: FilterInspect = %q, %v; a document of another shape must not be shown", name, out, ok)
		}
	}
	// A null Env or Config has nothing in it.
	if _, ok := hostexec.FilterInspect(`[{"Config":null},{"Config":{"Env":null}}]`); !ok {
		t.Error("null Config or Env was refused")
	}
}

func TestIsInspect(t *testing.T) {
	if !hostexec.IsInspect([]string{"docker", "inspect", "remedy-a"}) {
		t.Error("docker inspect is not recognised")
	}
	for _, argv := range [][]string{{"docker", "ps", "-a"}, {"inspect"}, {"df", "-h"}, nil} {
		if hostexec.IsInspect(argv) {
			t.Errorf("IsInspect(%q) = true", argv)
		}
	}
}
