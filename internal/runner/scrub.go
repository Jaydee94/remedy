package runner

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
)

// redactedKey replaces an API key in everything the runner posts.
const redactedKey = "[REDACTED:api-key]"

// minFragmentKey is the shortest key whose first and last six characters are scrubbed on their own. A shorter key is
// scrubbed only as a whole: six characters of it would be too common a word to hide.
const minFragmentKey = 24

// Scrubber removes the runner's API key from text. The runner is the only place that can: it knows the exact value, and the
// control plane must never learn it. A nil *Scrubber (no key, the subscription mode) changes nothing.
//
// It replaces every literal occurrence of the key, and its first and its last six characters where they stand alone (not
// inside a longer word: a CLI that prints a truncated key prints one of those). The replacement has no quote or backslash,
// so a scrubbed JSON line stays valid JSON. A key consists of letters, digits, '-' and '_', which JSON never escapes, so a
// key inside a JSON string is found as it is.
type Scrubber struct {
	key, head, tail string
}

// NewScrubber returns the scrubber of key, or nil for an empty key.
func NewScrubber(key string) *Scrubber {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	s := &Scrubber{key: key}
	if len(key) >= minFragmentKey {
		s.head, s.tail = key[:6], key[len(key)-6:]
	}
	return s
}

// scrubberFor returns the scrubber of the key in env when the mode is the key mode, and nil in every other case.
func scrubberFor(mode provider.Mode, env []string) *Scrubber {
	if mode != provider.ModeAPIKey {
		return nil
	}
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "ANTHROPIC_API_KEY="); ok {
			return NewScrubber(v)
		}
	}
	return nil
}

func isTokenChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'
}

// replaceStandalone replaces the occurrences of frag that are not part of a longer token.
func replaceStandalone(in, frag string) string {
	var b strings.Builder
	pos := 0
	for {
		i := strings.Index(in[pos:], frag)
		if i < 0 {
			b.WriteString(in[pos:])
			return b.String()
		}
		i += pos
		end := i + len(frag)
		alone := (i == 0 || !isTokenChar(in[i-1])) && (end == len(in) || !isTokenChar(in[end]))
		b.WriteString(in[pos:i])
		if alone {
			b.WriteString(redactedKey)
		} else {
			b.WriteString(frag)
		}
		pos = end
	}
}

// String returns in without the key.
func (s *Scrubber) String(in string) string {
	if s == nil {
		return in
	}
	out := strings.ReplaceAll(in, s.key, redactedKey)
	if s.head != "" {
		out = replaceStandalone(out, s.head)
		out = replaceStandalone(out, s.tail)
	}
	return out
}

// Bytes returns in without the key; a nil scrubber returns in itself.
func (s *Scrubber) Bytes(in []byte) []byte {
	if s == nil {
		return in
	}
	return []byte(s.String(string(in)))
}

type scrubSink struct {
	inner Sink
	s     *Scrubber
}

func (k scrubSink) Event(ctx context.Context, kind string, payload json.RawMessage) error {
	return k.inner.Event(ctx, kind, k.s.Bytes(payload))
}

// Sink wraps inner so that no event reaches it with the key in it.
func (s *Scrubber) Sink(inner Sink) Sink {
	if s == nil {
		return inner
	}
	return scrubSink{inner: inner, s: s}
}

// Outcome returns o with the key removed from the text the CLI wrote: the result and the structured answer.
func (s *Scrubber) Outcome(o run.Outcome) run.Outcome {
	if s == nil {
		return o
	}
	o.Result = s.String(o.Result)
	if len(o.Output) > 0 {
		o.Output = s.Bytes(o.Output)
	}
	return o
}
