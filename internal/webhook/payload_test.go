package webhook_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/webhook"
)

const good = `{"version":1,"key":"disk-full/nas","status":"firing","title":"Disk almost full on nas","severity":"warning",
"labels":{"instance":"nas"},"annotations":{"summary":"90% used"},"url":"https://monitor.example/alerts/42","startsAt":"2026-10-21T08:00:00Z"}`

func decode(body string) (webhook.Event, error) { return webhook.Decode(strings.NewReader(body)) }

func TestAGoodBodyBecomesAnEvent(t *testing.T) {
	e, err := decode(good)
	if err != nil {
		t.Fatal(err)
	}
	if e.Key != "disk-full/nas" || e.Title != "Disk almost full on nas" || e.Severity != "warning" || !e.Firing() || e.Labels["instance"] != "nas" ||
		e.URL != "https://monitor.example/alerts/42" || !e.StartsAt.Equal(time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("event = %+v", e)
	}
	var d struct {
		Labels       map[string]string `json:"labels"`
		Annotations  map[string]string `json:"annotations"`
		GeneratorURL string            `json:"generatorURL"`
		StartsAt     string            `json:"startsAt"`
	}
	if err := json.Unmarshal(e.Details, &d); err != nil || d.Labels["instance"] != "nas" || d.Annotations["summary"] != "90% used" ||
		d.GeneratorURL != e.URL || d.StartsAt != "2026-10-21T08:00:00Z" {
		t.Fatalf("details = %s, %v", e.Details, err)
	}
}

func TestOnlyVersionKeyAndStatusAreRequiredAndTheRestHasDefaults(t *testing.T) {
	e, err := decode(`{"version":1,"key":"k","status":"resolved"}`)
	if err != nil || e.Firing() || e.Severity != "none" || e.Title != "" {
		t.Fatalf("event = %+v, %v", e, err)
	}
	if _, err := decode(`{"version":1,"key":"k","status":"firing"}`); err == nil {
		t.Fatal("a firing event without a title was accepted")
	}
}

func TestEveryBadBodyIsRefusedAndNamesTheField(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	for name, c := range map[string]struct{ body, field string }{
		"no version":            {`{"key":"k","status":"resolved"}`, "version"},
		"version 2":             {`{"version":2,"key":"k","status":"resolved"}`, "version"},
		"version as a string":   {`{"version":"1","key":"k","status":"resolved"}`, "version"},
		"no key":                {`{"version":1,"status":"resolved"}`, "key"},
		"empty key":             {`{"version":1,"key":"","status":"resolved"}`, "key"},
		"key of 201":            {`{"version":1,"key":"` + long(201) + `","status":"resolved"}`, "key"},
		"key with a newline":    {`{"version":1,"key":"a\nb","status":"resolved"}`, "key"},
		"key with a tab":        {`{"version":1,"key":"a\tb","status":"resolved"}`, "key"},
		"key with a zero width": {`{"version":1,"key":"a​b","status":"resolved"}`, "key"},
		"key with an rlo":       {`{"version":1,"key":"a‮b","status":"resolved"}`, "key"},
		"key with a nbsp":       {`{"version":1,"key":"a b","status":"resolved"}`, "key"},
		"key redaction changes": {`{"version":1,"key":"AKIAIOSFODNN7EXAMPLE","status":"resolved"}`, "key"},
		"key null":              {`{"version":1,"key":null,"status":"resolved"}`, "key"},
		"no status":             {`{"version":1,"key":"k"}`, "status"},
		"status other":          {`{"version":1,"key":"k","status":"FIRING"}`, "status"},
		"severity other":        {`{"version":1,"key":"k","status":"resolved","severity":"high"}`, "severity"},
		"severity upper":        {`{"version":1,"key":"k","status":"resolved","severity":"Critical"}`, "severity"},
		"title as a number":     {`{"version":1,"key":"k","status":"resolved","title":5}`, "title"},
		"title as an object":    {`{"version":1,"key":"k","status":"resolved","title":{"a":1}}`, "title"},
		"labels as a list":      {`{"version":1,"key":"k","status":"resolved","labels":["a"]}`, "labels"},
		"label value nested":    {`{"version":1,"key":"k","status":"resolved","labels":{"a":{"b":"c"}}}`, "labels"},
		"label value null":      {`{"version":1,"key":"k","status":"resolved","labels":{"a":null}}`, "labels"},
		"label value a number":  {`{"version":1,"key":"k","status":"resolved","labels":{"a":1}}`, "labels"},
		"annotations nested":    {`{"version":1,"key":"k","status":"resolved","annotations":{"a":["b"]}}`, "annotations"},
		"url not http":          {`{"version":1,"key":"k","status":"resolved","url":"javascript:alert(1)"}`, "url"},
		"url relative":          {`{"version":1,"key":"k","status":"resolved","url":"/a"}`, "url"},
		"startsAt not a time":   {`{"version":1,"key":"k","status":"resolved","startsAt":"yesterday"}`, "startsAt"},
		"startsAt a number":     {`{"version":1,"key":"k","status":"resolved","startsAt":5}`, "startsAt"},
		"an unknown member":     {`{"version":1,"key":"k","status":"resolved","extra":1}`, "extra"},
		"a second value":        {`{"version":1,"key":"k","status":"resolved"} {}`, ""},
		"trailing garbage":      {`{"version":1,"key":"k","status":"resolved"}x`, ""},
		"not an object":         {`[1]`, ""},
		"empty body":            {``, ""},
		"not json":              {`status=firing`, ""},
	} {
		_, err := decode(c.body)
		var fe *webhook.FieldError
		if !errors.As(err, &fe) {
			t.Errorf("%s: error = %v, want a *FieldError", name, err)
			continue
		}
		if c.field != "" && fe.Field != c.field {
			t.Errorf("%s: field = %q, want %q", name, fe.Field, c.field)
		}
		if strings.Contains(fe.Error(), "AKIA") {
			t.Errorf("%s: the error repeats the submitted value: %v", name, fe)
		}
	}
}

func TestABodyOverTheLimitIsTooLargeWhateverItHolds(t *testing.T) {
	pad := strings.Repeat("a", webhook.MaxBody)
	_, err := decode(`{"version":1,"key":"k","status":"resolved","title":"` + pad + `"}`)
	if !errors.Is(err, webhook.ErrTooLarge) {
		t.Fatalf("error = %v, want ErrTooLarge", err)
	}
	_, err = decode(strings.Repeat(" ", webhook.MaxBody+1))
	if !errors.Is(err, webhook.ErrTooLarge) {
		t.Fatalf("a body of spaces: error = %v, want ErrTooLarge", err)
	}
}

func TestTextIsCleanedRedactedAndCut(t *testing.T) {
	e, err := decode(`{"version":1,"key":"k","status":"firing","title":"  a\n\tb  password=hunter2hunter2 ` + strings.Repeat("x", 300) + `",
		"labels":{"a":"` + strings.Repeat("v", 400) + `","AKIAIOSFODNN7EXAMPLE":"v","` + strings.Repeat("k", 65) + `":"v"},"annotations":{"d":"` + strings.Repeat("w", 1500) + `"},
		"url":"https://x.example/` + strings.Repeat("u", 3000) + `"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(e.Title)) > 200 || strings.ContainsAny(e.Title, "\n\t") || strings.Contains(e.Title, "hunter2") || !strings.HasPrefix(e.Title, "a b ") {
		t.Errorf("title = %q", e.Title)
	}
	var d struct {
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
		Truncated   bool              `json:"truncated"`
		URL         string            `json:"generatorURL"`
	}
	if err := json.Unmarshal(e.Details, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Labels) != 1 || len([]rune(d.Labels["a"])) != 300 || len([]rune(d.Annotations["d"])) != 1000 || len([]rune(d.URL)) != 2000 || !d.Truncated {
		t.Errorf("details = labels %d (a: %d), annotations %d, url %d, truncated %v", len(d.Labels), len([]rune(d.Labels["a"])), len(d.Annotations["d"]), len([]rune(d.URL)), d.Truncated)
	}
}

func TestMoreThanThirtyLabelsAreCutToThirty(t *testing.T) {
	var b strings.Builder
	for i := range 40 {
		fmt.Fprintf(&b, `"l%02d":"v",`, i)
	}
	e, err := decode(`{"version":1,"key":"k","status":"resolved","labels":{` + strings.TrimSuffix(b.String(), ",") + `}}`)
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Labels    map[string]string `json:"labels"`
		Truncated bool              `json:"truncated"`
	}
	_ = json.Unmarshal(e.Details, &d)
	if len(d.Labels) != 30 || !d.Truncated || len(e.Labels) != 40 {
		t.Errorf("stored labels %d, truncated %v, labels for the host rules %d", len(d.Labels), d.Truncated, len(e.Labels))
	}
}
