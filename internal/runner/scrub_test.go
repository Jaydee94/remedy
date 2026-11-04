package runner_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/runner"
	"github.com/Jaydee94/remedy/internal/testutil"
)

const testKey = "sk-ant-api03-AbCdEf0123456789xyzXYZ_-0987654321qrstuv"

func TestTheScrubberReplacesTheKeyWhereverItStands(t *testing.T) {
	s := runner.NewScrubber(testKey)
	const mark = "[REDACTED:api-key]"
	head, tail := testKey[:6], testKey[len(testKey)-6:]
	for name, c := range map[string]struct{ in, want string }{
		"in the middle":                    {"bad key " + testKey + " given", "bad key " + mark + " given"},
		"twice":                            {testKey + " and " + testKey, mark + " and " + mark},
		"at the start":                     {testKey + " rejected", mark + " rejected"},
		"at the end":                       {"rejected " + testKey, "rejected " + mark},
		"in a JSON string":                 {`{"error":"invalid x-api-key ` + testKey + `","n":1}`, `{"error":"invalid x-api-key ` + mark + `","n":1}`},
		"the first six, alone":             {"starts " + head + " ok", "starts " + mark + " ok"},
		"the last six, alone":              {"ends " + tail + " here", "ends " + mark + " here"},
		"the last six, quoted":             {`"` + tail + `"`, `"` + mark + `"`},
		"nothing":                          {"nothing to hide", "nothing to hide"},
		"the head in a longer word":        {"x" + head + "ique", "x" + head + "ique"},
		"the head after a token":           {"a" + head, "a" + head},
		"the same fragment twice in a row": {tail + tail, tail + tail},
	} {
		got := s.String(c.in)
		if got != c.want {
			t.Errorf("%s: String(%q) = %q, want %q", name, c.in, got, c.want)
		}
		if strings.Contains(got, testKey) {
			t.Errorf("%s: the key is still in %q", name, got)
		}
	}
}

func TestAShortKeyIsScrubbedOnlyAsAWhole(t *testing.T) {
	s := runner.NewScrubber("short-key-1234")
	if got := s.String("short-key-1234 and short- and 1234"); got != "[REDACTED:api-key] and short- and 1234" {
		t.Errorf("got %q", got)
	}
}

func TestTheScrubbedJSONStaysValid(t *testing.T) {
	s := runner.NewScrubber(testKey)
	in := []byte(`{"type":"result","result":"401: invalid key ` + testKey + `","nested":{"k":["` + testKey + `"]}}`)
	out := s.Bytes(in)
	if !json.Valid(out) {
		t.Fatalf("the scrubbed line is not valid JSON: %s", out)
	}
	if strings.Contains(string(out), testKey) {
		t.Fatalf("the key survived: %s", out)
	}
}

func TestNoKeyMeansNoChange(t *testing.T) {
	for _, s := range []*runner.Scrubber{nil, runner.NewScrubber(""), runner.NewScrubber("   ")} {
		if got := s.String("sk-ant plain text"); got != "sk-ant plain text" {
			t.Errorf("the identity scrubber changed the text: %q", got)
		}
		if got := string(s.Bytes([]byte(`{"a":1}`))); got != `{"a":1}` {
			t.Errorf("the identity scrubber changed the bytes: %q", got)
		}
	}
}

// The whole path: what the CLI prints on stdout, on stderr and in its result reaches the control plane without the key.
func TestExecuteWithAScrubberPostsNoKey(t *testing.T) {
	bin := testutil.ScriptClaude(t, `cat >/dev/null
echo '{"type":"system","subtype":"init","apiKeySource":"ANTHROPIC_API_KEY"}'
echo '{"type":"assistant","message":"the key is `+testKey+`"}'
echo 'warn: key `+testKey+` rejected' >&2
echo '{"type":"result","subtype":"error","result":"401 for `+testKey+`","session_id":"s","total_cost_usd":0}'
exit 1`)
	sink := &recordingSink{}
	scr := runner.NewScrubber(testKey)
	out, err := runner.Execute(context.Background(), provider.Claude{Binary: bin, Mode: provider.ModeAPIKey},
		provider.Spec{Prompt: "p", Workdir: t.TempDir()}, append(os.Environ(), "ANTHROPIC_API_KEY="+testKey), scr.Sink(sink))
	if err != nil {
		t.Fatal(err)
	}
	out = scr.Outcome(out)
	if len(sink.events) < 4 {
		t.Fatalf("events = %+v, want the init, the message, the stderr line and the result", sink.events)
	}
	for _, e := range sink.events {
		if strings.Contains(e.Payload, testKey) || strings.Contains(e.Payload, "AbCdEf0123456789") {
			t.Errorf("an event of kind %s carries the key: %s", e.Kind, e.Payload)
		}
		if e.Kind != "stderr" && !json.Valid([]byte(e.Payload)) {
			t.Errorf("an event of kind %s is not valid JSON after the scrub: %s", e.Kind, e.Payload)
		}
	}
	if strings.Contains(out.Result, testKey) || out.Result != "401 for [REDACTED:api-key]" {
		t.Errorf("Result = %q, want the key replaced", out.Result)
	}
	var _ run.Outcome = out
}
