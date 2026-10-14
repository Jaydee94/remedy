package settings

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Kind is the type of a setting.
type Kind string

const (
	KindDuration Kind = "duration"
	KindNumber   Kind = "number"
	KindList     Kind = "list"
)

// Value is the value of one setting. Only the field of the spec's Kind is used.
type Value struct {
	Dur  time.Duration
	Num  int
	List []string
}

// JSON is the form that is stored and sent: a string for a duration ("15m"), a number, an array of strings.
func (v Value) JSON(k Kind) json.RawMessage {
	var b []byte
	switch k {
	case KindDuration:
		b, _ = json.Marshal(formatDuration(v.Dur))
	case KindNumber:
		b = []byte(strconv.Itoa(v.Num))
	default:
		list := v.List
		if list == nil {
			list = []string{}
		}
		b, _ = json.Marshal(list)
	}
	return b
}

// Text is the form a person reads, in the activity entries and the logs: "15m", "3", "demo,staging", "none".
func (v Value) Text(k Kind) string {
	switch k {
	case KindDuration:
		return formatDuration(v.Dur)
	case KindNumber:
		return strconv.Itoa(v.Num)
	}
	if len(v.List) == 0 {
		return "none"
	}
	return strings.Join(v.List, ",")
}

// formatDuration writes a duration in whole units: 1h, 15m, 90s; one that is not a whole number of seconds in Go's own form.
func formatDuration(d time.Duration) string {
	switch {
	case d > 0 && d%time.Hour == 0:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d > 0 && d%time.Minute == 0:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d > 0 && d%time.Second == 0:
		return strconv.Itoa(int(d/time.Second)) + "s"
	}
	return d.String()
}
