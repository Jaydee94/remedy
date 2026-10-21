package loginrelay

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const plantedURL = "https://claude.com/cai/oauth/authorize?planted-url-7x"

func TestDecodeReportIsStrict(t *testing.T) {
	ok := fmt.Sprintf(`{"session":"s1","state":"awaiting_code","attempt":1,"url":%q}`, validURL)
	r, err := DecodeReport(strings.NewReader(ok))
	if err != nil {
		t.Fatal(err)
	}
	if r.Session != "s1" || r.State != "awaiting_code" || r.Attempt != 1 || r.URL != validURL {
		t.Fatalf("report = %+v", r)
	}
	r, err = DecodeReport(strings.NewReader(`{"session":"s1","state":"failed","attempt":3,"reason":"invalid_code"}`))
	if err != nil || r.Reason != ReasonInvalidCode {
		t.Fatalf("failed report: %+v %v", r, err)
	}
	if _, err = DecodeReport(strings.NewReader(`{"session":"s1","state":"succeeded","attempt":2}`)); err != nil {
		t.Fatalf("succeeded report: %v", err)
	}
	// Trailing white space is not a second object.
	if _, err = DecodeReport(strings.NewReader(`{"session":"s1","state":"succeeded","attempt":2}` + "\n")); err != nil {
		t.Fatalf("trailing newline: %v", err)
	}

	bad := map[string]string{
		"unknown field":         `{"session":"s1","state":"succeeded","attempt":1,"extra":1}`,
		"second object":         `{"session":"s1","state":"succeeded","attempt":1}{"session":"s2"}`,
		"trailing garbage":      `{"session":"s1","state":"succeeded","attempt":1} x`,
		"over 4 KiB":            `{"session":"` + strings.Repeat("a", 4096) + `","state":"succeeded","attempt":1}`,
		"state starting":        `{"session":"s1","state":"starting","attempt":1}`,
		"state empty":           `{"session":"s1","state":"","attempt":1}`,
		"url with failed":       fmt.Sprintf(`{"session":"s1","state":"failed","attempt":1,"url":%q,"reason":"timeout"}`, plantedURL),
		"url with succeeded":    fmt.Sprintf(`{"session":"s1","state":"succeeded","attempt":1,"url":%q}`, plantedURL),
		"reason with awaiting":  `{"session":"s1","state":"awaiting_code","attempt":1,"reason":"timeout"}`,
		"reason with succeeded": `{"session":"s1","state":"succeeded","attempt":1,"reason":"timeout"}`,
		"reason cancelled":      `{"session":"s1","state":"failed","attempt":1,"reason":"cancelled"}`,
		"reason runner_lost":    `{"session":"s1","state":"failed","attempt":1,"reason":"runner_lost"}`,
		"reason unknown":        `{"session":"s1","state":"failed","attempt":1,"reason":"because"}`,
		"attempt 0":             `{"session":"s1","state":"succeeded","attempt":0}`,
		"attempt 4":             `{"session":"s1","state":"succeeded","attempt":4}`,
		"attempt missing":       `{"session":"s1","state":"succeeded"}`,
		"wrong type":            `{"session":1,"state":"succeeded","attempt":1}`,
		"not an object":         `[]`,
		"empty":                 ``,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeReport(strings.NewReader(body))
			if !errors.Is(err, ErrBadBody) {
				t.Fatalf("want ErrBadBody, got %v", err)
			}
			if strings.Contains(err.Error(), "planted-url-7x") {
				t.Fatal("the error holds the url")
			}
		})
	}
}

func TestDecodeCodeRequestIsStrict(t *testing.T) {
	session, code, err := DecodeCodeRequest(strings.NewReader(`{"session":"s1","code":" AbC123-_xyz\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	if session != "s1" || string(code.Bytes()) != "AbC123-_xyz" {
		t.Fatalf("session %q", session)
	}
	if strings.Contains(fmt.Sprintf("%v %s", code, code), "AbC123") {
		t.Fatal("the code prints")
	}

	for name, body := range map[string]string{
		"unknown field": `{"session":"s1","code":"AbC123-_xyz","extra":1}`,
		"second object": `{"session":"s1","code":"AbC123-_xyz"}{}`,
		"over 1 KiB":    `{"session":"s1","code":"AbC123-_xyz","x":"` + strings.Repeat("a", 1024) + `"}`,
		"empty":         ``,
		"wrong type":    `{"session":"s1","code":5}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := DecodeCodeRequest(strings.NewReader(body))
			if !errors.Is(err, ErrBadBody) {
				t.Fatalf("want ErrBadBody, got %v", err)
			}
		})
	}

	_, _, err = DecodeCodeRequest(strings.NewReader(`{"session":"s1","code":"abcd efgh-planted"}`))
	if !errors.Is(err, ErrBadCode) {
		t.Fatalf("want ErrBadCode, got %v", err)
	}
	if strings.Contains(err.Error(), "planted") || strings.Contains(err.Error(), "abcd") {
		t.Fatal("the error holds the code")
	}
	_, _, err = DecodeCodeRequest(strings.NewReader(`{"session":"s1","code":"short"}`))
	if !errors.Is(err, ErrBadCode) {
		t.Fatalf("want ErrBadCode, got %v", err)
	}
}

func TestDecodePoll(t *testing.T) {
	p, err := DecodePoll(strings.NewReader(`{"boot":"b1","session":"s1","ack":7}`))
	if err != nil {
		t.Fatal(err)
	}
	if p != (PollRequest{Boot: "b1", Session: "s1", Ack: 7}) {
		t.Fatalf("poll = %+v", p)
	}
	if _, err = DecodePoll(strings.NewReader(`{"boot":"b1"}`)); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	for name, body := range map[string]string{
		"unknown field": `{"boot":"b1","session":"s1","ack":0,"extra":1}`,
		"negative ack":  `{"boot":"b1","session":"s1","ack":-1}`,
		"second object": `{"boot":"b1"}{"boot":"b2"}`,
		"empty":         ``,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePoll(strings.NewReader(body)); !errors.Is(err, ErrBadBody) {
				t.Fatalf("want ErrBadBody, got %v", err)
			}
		})
	}
}

func TestCommandWireJSON(t *testing.T) {
	c := Command{Seq: 3, Session: "s1", Action: ActionCode, Code: NewMaterial(planted)}

	// A stray Marshal prints stars: Command has no MarshalJSON of its own, so
	// the holder inside answers.
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), planted) {
		t.Fatalf("json.Marshal leaks: %s", b)
	}

	var a PollAnswer
	if err := json.Unmarshal(c.WireJSON(), &a); err != nil {
		t.Fatal(err)
	}
	if a.Seq != 3 || a.Session != "s1" || a.Action != "code" || a.Code != planted {
		t.Fatalf("answer = %+v", a)
	}

	start := Command{Seq: 1, Session: "s1", Action: ActionStart}
	if strings.Contains(string(start.WireJSON()), `"code"`) {
		t.Fatalf("a start carries a code field: %s", start.WireJSON())
	}
	if ActionStart != "start" || ActionCode != "code" || ActionCancel != "cancel" {
		t.Fatal("action words changed")
	}
}
