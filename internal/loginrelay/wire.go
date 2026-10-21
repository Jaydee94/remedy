package loginrelay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
)

const (
	maxPollBytes   = 4 << 10
	maxReportBytes = 4 << 10
	maxCodeBody    = 1 << 10
)

// Action is what the control plane tells the runner to do.
type Action string

const (
	ActionStart  Action = "start"
	ActionCode   Action = "code"
	ActionCancel Action = "cancel"
)

// PollRequest is the runner's long poll: its boot id, the session it holds
// and the last command number it has acted on.
type PollRequest struct {
	Boot    string `json:"boot"`
	Session string `json:"session"`
	Ack     int64  `json:"ack"`
}

// Command is a command in the control plane's memory. It has no MarshalJSON of
// its own, so a stray json.Marshal prints "***" for the code; WireJSON is the
// only place that unmasks it.
type Command struct {
	Seq     int64
	Session string
	Action  Action
	Code    Material
}

// WireJSON is the body of the poll answer. The code leaves the holder here and
// nowhere else.
func (c Command) WireJSON() []byte {
	a := PollAnswer{Seq: c.Seq, Session: c.Session, Action: string(c.Action)}
	if c.Code.Len() > 0 {
		a.Code = string(c.Code.Bytes())
	}
	b, err := json.Marshal(a)
	if err != nil {
		return nil // unreachable: strings and integers only
	}
	return b
}

// PollAnswer is what the runner decodes the poll answer into; it wraps Code in
// a Material at once.
type PollAnswer struct {
	Seq     int64  `json:"seq"`
	Session string `json:"session"`
	Action  string `json:"action"`
	Code    string `json:"code,omitempty"`
}

// Report is the runner's state report. URL is set only with awaiting_code,
// Reason only with failed.
type Report struct {
	Session string `json:"session"`
	State   string `json:"state"`
	Attempt int    `json:"attempt"`
	URL     string `json:"url,omitempty"`
	Reason  Reason `json:"reason,omitempty"`
}

// URL and Code are plain strings because they are JSON decode targets. The
// methods below keep them out of fmt and slog: every verb and every handler
// shows stars in their place. JSON encoding is unchanged, the runner needs the
// url on the wire.

func (a PollAnswer) text() string {
	return fmt.Sprintf("{seq=%d session=%s action=%s code=%s}", a.Seq, a.Session, a.Action, stars)
}

// Format writes the answer with the code masked, for every verb.
func (a PollAnswer) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(a.text())) }

// String is the answer with the code masked.
func (a PollAnswer) String() string { return a.text() }

// LogValue keeps the code out of slog.
func (a PollAnswer) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int64("seq", a.Seq), slog.String("session", a.Session),
		slog.String("action", a.Action), slog.String("code", stars))
}

func (r Report) text() string {
	return fmt.Sprintf("{session=%s state=%s attempt=%d url=%s reason=%s}", r.Session, r.State, r.Attempt, stars, string(r.Reason))
}

// Format writes the report with the url masked, for every verb.
func (r Report) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(r.text())) }

// String is the report with the url masked.
func (r Report) String() string { return r.text() }

// LogValue keeps the url out of slog.
func (r Report) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("session", r.Session), slog.String("state", r.State),
		slog.Int("attempt", r.Attempt), slog.String("url", stars),
		slog.String("reason", string(r.Reason)))
}

// decodeStrict reads at most max bytes, decodes exactly one JSON object into v
// with unknown fields refused and nothing but white space after it. It returns
// ErrBadBody for every failure and never the decoder's error, which may quote
// the body. The body (for the code route, the code) is cleared on every return;
// the decoded strings are copies.
func decodeStrict(r io.Reader, max int64, v any) error {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	defer clear(data)
	if err != nil || int64(len(data)) > max {
		return ErrBadBody
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return ErrBadBody
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrBadBody
	}
	return nil
}

// DecodePoll reads a poll request (4 KiB, strict). The boot is required: a
// session first served to an empty boot could never see a runner restart.
func DecodePoll(r io.Reader) (PollRequest, error) {
	var p PollRequest
	if err := decodeStrict(r, maxPollBytes, &p); err != nil {
		return PollRequest{}, err
	}
	if p.Boot == "" || p.Ack < 0 {
		return PollRequest{}, ErrBadBody
	}
	return p, nil
}

// DecodeReport reads a runner report (4 KiB, strict). The state is one of
// awaiting_code, succeeded, failed; a url comes only with awaiting_code and a
// reason only with failed and one a runner may report; the attempt is 1 to 3.
// The url itself is judged by ValidateURL, not here.
func DecodeReport(r io.Reader) (Report, error) {
	var rep Report
	if err := decodeStrict(r, maxReportBytes, &rep); err != nil {
		return Report{}, err
	}
	switch State(rep.State) {
	case StateAwaitingCode, StateSucceeded, StateFailed:
	default:
		return Report{}, ErrBadBody
	}
	if rep.Attempt < 1 || rep.Attempt > 3 {
		return Report{}, ErrBadBody
	}
	if rep.URL != "" && State(rep.State) != StateAwaitingCode {
		return Report{}, ErrBadBody
	}
	if rep.Reason != "" && (State(rep.State) != StateFailed || !rep.Reason.FromRunner()) {
		return Report{}, ErrBadBody
	}
	return rep, nil
}

// DecodeCodeRequest reads the browser's code submission (1 KiB, strict) and
// validates the code. The code comes back in a Material, trimmed.
func DecodeCodeRequest(r io.Reader) (session string, code Material, err error) {
	var body struct {
		Session string `json:"session"`
		Code    string `json:"code"`
	}
	if err := decodeStrict(r, maxCodeBody, &body); err != nil {
		return "", Material{}, err
	}
	trimmed, err := ValidateCode(body.Code)
	if err != nil {
		return "", Material{}, err
	}
	return body.Session, NewMaterial(trimmed), nil
}
