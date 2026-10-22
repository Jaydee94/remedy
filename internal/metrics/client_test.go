package metrics_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/metrics"
	"github.com/Jaydee94/remedy/internal/metrics/metricstest"
	"github.com/Jaydee94/remedy/internal/secret"
)

func creds(url, token string) metrics.Credentials {
	return func() (string, secret.Value, error) { return url, secret.NewValue(token), nil }
}

func newClient(t *testing.T, c metrics.Credentials) *metrics.Client {
	t.Helper()
	cl, err := metrics.New(c, "")
	if err != nil {
		t.Fatal(err)
	}
	return cl
}

var ctx = context.Background()

func TestTheBasePathIsKeptAndTheInstantQueryIsAGet(t *testing.T) {
	fake := metricstest.New(t, "")
	at := time.Unix(1700000000, 500_000_000)
	if _, err := newClient(t, creds(fake.URL+"/select/0/prometheus/", "")).Query(ctx, `up{job="x"}`, at); err != nil {
		t.Fatal(err)
	}
	reqs := fake.Requests()
	if len(reqs) != 1 || reqs[0].Method != http.MethodGet || reqs[0].Path != "/select/0/prometheus/api/v1/query" {
		t.Fatalf("requests = %+v", reqs)
	}
	q, _ := url.ParseQuery(reqs[0].Query)
	if q.Get("query") != `up{job="x"}` || q.Get("time") != "1700000000.500" {
		t.Errorf("query = %v", q)
	}
}

func TestAnInstantQueryWithoutATimeLeavesItToTheServer(t *testing.T) {
	fake := metricstest.New(t, "")
	if _, err := newClient(t, creds(fake.URL, "")).Query(ctx, "up", time.Time{}); err != nil {
		t.Fatal(err)
	}
	q, _ := url.ParseQuery(fake.Requests()[0].Query)
	if _, has := q["time"]; has || q.Get("query") != "up" {
		t.Errorf("query = %v", q)
	}
}

func TestARangeQueryCarriesStartEndAndStep(t *testing.T) {
	fake := metricstest.New(t, "")
	start := time.Unix(1700000000, 0)
	if _, err := newClient(t, creds(fake.URL+"/base", "")).QueryRange(ctx, "up", start, start.Add(time.Hour), 90*time.Second); err != nil {
		t.Fatal(err)
	}
	r := fake.Requests()[0]
	q, _ := url.ParseQuery(r.Query)
	if r.Path != "/base/api/v1/query_range" || q.Get("start") != "1700000000.000" || q.Get("end") != "1700003600.000" || q.Get("step") != "90" {
		t.Errorf("%s ? %v", r.Path, q)
	}
}

func TestARangeThatCannotBeAskedForIsRefusedBeforeItIsSent(t *testing.T) {
	fake := metricstest.New(t, "")
	cl := newClient(t, creds(fake.URL, ""))
	now := time.Now()
	for name, f := range map[string]func() error{
		"no step":       func() error { _, err := cl.QueryRange(ctx, "up", now.Add(-time.Hour), now, 0); return err },
		"end before":    func() error { _, err := cl.QueryRange(ctx, "up", now, now.Add(-time.Hour), time.Minute); return err },
		"empty expr":    func() error { _, err := cl.QueryRange(ctx, "", now.Add(-time.Hour), now, time.Minute); return err },
		"empty instant": func() error { _, err := cl.Query(ctx, "  ", now); return err },
	} {
		if err := f(); err == nil {
			t.Errorf("%s was sent", name)
		}
	}
	if n := len(fake.Requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestATokenIsSentOnlyWhenSetAndNeverShownInAnError(t *testing.T) {
	fake := metricstest.New(t, "tok-SECRET-123456")
	if _, err := newClient(t, creds(fake.URL, "tok-SECRET-123456")).Query(ctx, "up", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if got := fake.Requests()[0].Auth; got != "Bearer tok-SECRET-123456" {
		t.Errorf("auth = %q", got)
	}
	plain := metricstest.New(t, "")
	if _, err := newClient(t, creds(plain.URL, "")).Query(ctx, "up", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if got := plain.Requests()[0].Auth; got != "" {
		t.Errorf("auth without a token = %q", got)
	}
	// a wrong token: the answer is an error, and no error shows the token
	_, err := newClient(t, creds(fake.URL, "tok-WRONG-0000000")).Query(ctx, "up", time.Time{})
	if err == nil || strings.Contains(err.Error(), "tok-WRONG-0000000") || strings.Contains(err.Error(), "tok-SECRET-123456") {
		t.Errorf("error = %v", err)
	}
	// an unreachable server
	dead := httptest.NewServer(http.NotFoundHandler())
	u := dead.URL
	dead.Close()
	if err := newClient(t, creds(u, "tok-SECRET-123456")).Ping(ctx); err == nil || strings.Contains(err.Error(), "tok-SECRET-123456") {
		t.Errorf("unreachable: %v", err)
	}
	// a server that echoes the token in its message
	plain.SetRaw(400, `{"status":"error","errorType":"bad_data","error":"bad token tok-ECHOED-1234567 here"}`)
	_, err = newClient(t, creds(plain.URL, "tok-ECHOED-1234567")).Query(ctx, "up", time.Time{})
	if err == nil || strings.Contains(err.Error(), "tok-ECHOED-1234567") {
		t.Errorf("echoed: %v", err)
	}
}

func TestTheAddressAndTheTokenAreAskedForAtEveryRequest(t *testing.T) {
	fake := metricstest.New(t, "")
	n := 0
	cl := newClient(t, func() (string, secret.Value, error) {
		n++
		return fake.URL, secret.NewValue(fmt.Sprintf("t%d", n)), nil
	})
	_, _ = cl.Query(ctx, "up", time.Time{})
	_ = cl.Ping(ctx)
	reqs := fake.Requests()
	if len(reqs) != 2 || reqs[0].Auth != "Bearer t1" || reqs[1].Auth != "Bearer t2" {
		t.Fatalf("requests = %+v", reqs)
	}
}

func TestAnEmptyAddressIsNotConfigured(t *testing.T) {
	cl := newClient(t, creds("", ""))
	if _, err := cl.Query(ctx, "up", time.Time{}); !errors.Is(err, metrics.ErrNotConfigured) {
		t.Fatalf("error = %v", err)
	}
	if _, err := cl.QueryRange(ctx, "up", time.Now().Add(-time.Hour), time.Now(), time.Minute); !errors.Is(err, metrics.ErrNotConfigured) {
		t.Fatalf("error = %v", err)
	}
	if err := cl.Ping(ctx); !errors.Is(err, metrics.ErrNotConfigured) {
		t.Fatalf("error = %v", err)
	}
	boom := errors.New("cannot open the token")
	cl = newClient(t, func() (string, secret.Value, error) { return "", secret.Value{}, boom })
	if _, err := cl.Query(ctx, "up", time.Time{}); !errors.Is(err, boom) {
		t.Fatalf("error = %v", err)
	}
}

func TestARedirectIsNotFollowed(t *testing.T) {
	var elsewhere int
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { elsewhere++ }))
	t.Cleanup(other.Close)
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	t.Cleanup(redirecting.Close)
	cl := newClient(t, creds(redirecting.URL, "tok"))
	for name, f := range map[string]func() error{
		"query": func() error { _, err := cl.Query(ctx, "up", time.Time{}); return err },
		"ping":  func() error { return cl.Ping(ctx) },
	} {
		var api *metrics.APIError
		if err := f(); !errors.As(err, &api) || api.Status != http.StatusFound {
			t.Errorf("%s: error = %v", name, err)
		}
	}
	if elsewhere != 0 {
		t.Fatalf("%d requests went to the other server", elsewhere)
	}
}

func TestAVectorParses(t *testing.T) {
	fake := metricstest.New(t, "")
	fake.SetRaw(200, `{"status":"success","data":{"resultType":"vector","result":[
	  {"metric":{"__name__":"up","job":"node","instance":"a:9100"},"value":[1700000000.5,"1"]},
	  {"metric":{"job":"x"},"value":[1700000001,"0.25"]}]}}`)
	got, err := newClient(t, creds(fake.URL, "")).Query(ctx, "up", time.Time{})
	if err != nil || got.Type != "vector" || got.Truncated || len(got.Series) != 2 {
		t.Fatalf("%+v, %v", got, err)
	}
	s := got.Series[0]
	if !reflect.DeepEqual(s.Labels, map[string]string{"__name__": "up", "job": "node", "instance": "a:9100"}) || len(s.Samples) != 1 ||
		!s.Samples[0].At.Equal(time.Unix(1700000000, 500_000_000)) || s.Samples[0].Value != 1 {
		t.Errorf("series = %+v", s)
	}
	if got.Series[1].Samples[0].Value != 0.25 {
		t.Errorf("series = %+v", got.Series[1])
	}
}

func TestAMatrixParses(t *testing.T) {
	fake := metricstest.New(t, "")
	fake.SetRaw(200, `{"status":"success","isPartial":false,"data":{"resultType":"matrix","result":[
	  {"metric":{"job":"node"},"values":[[1700000000,"1"],[1700000060,"2"],[1700000120,"3"]]}]},"stats":{"seriesFetched":"1"}}`)
	start := time.Unix(1700000000, 0)
	got, err := newClient(t, creds(fake.URL, "")).QueryRange(ctx, "up", start, start.Add(2*time.Minute), time.Minute)
	if err != nil || got.Type != "matrix" || len(got.Series) != 1 || len(got.Series[0].Samples) != 3 {
		t.Fatalf("%+v, %v", got, err)
	}
	if s := got.Series[0].Samples[2]; s.Value != 3 || !s.At.Equal(time.Unix(1700000120, 0)) {
		t.Errorf("sample = %+v", s)
	}
}

func TestAScalarParses(t *testing.T) {
	fake := metricstest.New(t, "")
	fake.SetRaw(200, `{"status":"success","data":{"resultType":"scalar","result":[1700000000,"42"]}}`)
	got, err := newClient(t, creds(fake.URL, "")).Query(ctx, "scalar(up)", time.Time{})
	if err != nil || got.Type != "scalar" || len(got.Series) != 1 || len(got.Series[0].Labels) != 0 || got.Series[0].Samples[0].Value != 42 {
		t.Fatalf("%+v, %v", got, err)
	}
}

func TestNaNAndInfinitySamplesParse(t *testing.T) {
	fake := metricstest.New(t, "")
	fake.SetRaw(200, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1,"NaN"],[2,"+Inf"],[3,"-Inf"]]}]}}`)
	start := time.Unix(1, 0)
	got, err := newClient(t, creds(fake.URL, "")).QueryRange(ctx, "x", start, start.Add(time.Hour), time.Minute)
	if err != nil || len(got.Series) != 1 {
		t.Fatalf("%+v, %v", got, err)
	}
	v := got.Series[0].Samples
	if !math.IsNaN(v[0].Value) || !math.IsInf(v[1].Value, 1) || !math.IsInf(v[2].Value, -1) {
		t.Errorf("samples = %+v", v)
	}
}

func TestAnErrorAnswerBecomesAFixedErrorWithAClippedMessage(t *testing.T) {
	fake := metricstest.New(t, "")
	long := strings.Repeat("x", 1000)
	for _, status := range []int{200, 400, 422} {
		fake.SetRaw(status, `{"status":"error","errorType":"bad_data","error":"parse error: unexpected end\nof input `+long+`"}`)
		_, err := newClient(t, creds(fake.URL, "")).Query(ctx, "up{", time.Time{})
		var qe *metrics.QueryError
		if !errors.As(err, &qe) {
			t.Fatalf("%d: error = %v", status, err)
		}
		if !strings.HasPrefix(qe.Message, "parse error: unexpected end of input ") || len([]rune(qe.Message)) > 200 || strings.ContainsAny(qe.Message, "\n\r") {
			t.Errorf("%d: message = %q", status, qe.Message)
		}
		if !strings.HasPrefix(err.Error(), "metrics: the query was refused: ") || len(err.Error()) > 300 {
			t.Errorf("%d: text = %q", status, err.Error())
		}
	}
}

func TestAStatusOtherThan2xxWithoutAnErrorBodyIsAnAPIErrorWithoutTheBody(t *testing.T) {
	fake := metricstest.New(t, "")
	fake.SetRaw(502, `<html>internal secret detail</html>`)
	_, err := newClient(t, creds(fake.URL, "")).Query(ctx, "up", time.Time{})
	var api *metrics.APIError
	if !errors.As(err, &api) || api.Status != 502 || strings.Contains(err.Error(), "secret detail") {
		t.Fatalf("error = %v", err)
	}
	if err := newClient(t, creds(fake.URL, "")).Ping(ctx); !errors.As(err, &api) || api.Status != 502 {
		t.Fatalf("ping: %v", err)
	}
}

func TestAnAnswerThatIsNotAQueryResultIsAnErrorThatQuotesNothing(t *testing.T) {
	fake := metricstest.New(t, "")
	for name, body := range map[string]string{
		"html": `<html>secret detail`, "empty": ``, "array": `[]`, "null": `null`, "no data": `{"status":"success"}`,
		"string type":      `{"status":"success","data":{"resultType":"string","result":[1,"x"]}}`,
		"no status":        `{"data":{"resultType":"vector","result":[]}}`,
		"bad sample":       `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"abc"]}]}}`,
		"bad time":         `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":["x","1"]}]}}`,
		"cut off in front": `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"a":"b"},"val`,
	} {
		fake.SetRaw(200, body)
		got, err := newClient(t, creds(fake.URL, "")).Query(ctx, "up", time.Time{})
		if err == nil || !errors.Is(err, metrics.ErrNotMetrics) || strings.Contains(err.Error(), "secret detail") || got.Series != nil {
			t.Errorf("%s: %+v, %v", name, got, err)
		}
	}
}

func TestAnAnswerOverTheByteLimitIsCutAndSaysSo(t *testing.T) {
	fake := metricstest.New(t, "")
	var b strings.Builder
	b.WriteString(`{"status":"success","data":{"resultType":"matrix","result":[`)
	for i := 0; b.Len() < 5<<20; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"metric":{"instance":"host-%06d"},"values":[[1700000000,"1"],[1700000060,"2"]]}`, i)
	}
	b.WriteString(`]}}`)
	fake.SetRaw(200, b.String())
	start := time.Unix(1700000000, 0)
	got, err := newClient(t, creds(fake.URL, "")).QueryRange(ctx, "up", start, start.Add(time.Hour), time.Minute)
	// every series has the same length, so the cut falls in a known one: the series that ended before the 4 MiB are kept, no more
	head := len(`{"status":"success","data":{"resultType":"matrix","result":[`)
	entry := len(fmt.Sprintf(`{"metric":{"instance":"host-%06d"},"values":[[1700000000,"1"],[1700000060,"2"]]}`, 0))
	want := (4<<20 - head + 1) / (entry + 1)
	if err != nil || !got.Truncated || len(got.Series) != want {
		t.Fatalf("%d series (want %d), truncated = %v, %v", len(got.Series), want, got.Truncated, err)
	}
	last := got.Series[len(got.Series)-1]
	if len(last.Samples) != 2 || last.Labels["instance"] == "" {
		t.Errorf("the last kept series is not whole: %+v", last)
	}
	if got.Series[0].Labels["instance"] != "host-000000" {
		t.Errorf("the first series is not the first of the answer: %+v", got.Series[0])
	}
}

func TestAnAnswerThatFitsIsNotTruncated(t *testing.T) {
	fake := metricstest.New(t, "")
	fake.SetRaw(200, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"a":"b"},"value":[1,"2"]}]}}`)
	got, err := newClient(t, creds(fake.URL, "")).Query(ctx, "up", time.Time{})
	if err != nil || got.Truncated {
		t.Fatalf("%+v, %v", got, err)
	}
}

func TestPingAsksForTheBuildInfo(t *testing.T) {
	fake := metricstest.New(t, "tok")
	if err := newClient(t, creds(fake.URL+"/prom", "tok")).Ping(ctx); err != nil {
		t.Fatal(err)
	}
	r := fake.Requests()
	if len(r) != 1 || r[0].Method != http.MethodGet || r[0].Path != "/prom/api/v1/status/buildinfo" || r[0].Auth != "Bearer tok" {
		t.Fatalf("requests = %+v", r)
	}
}

func TestTheClientOnlyHasReadingMethods(t *testing.T) {
	typ := reflect.TypeOf(&metrics.Client{})
	var names []string
	for i := 0; i < typ.NumMethod(); i++ {
		names = append(names, typ.Method(i).Name)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"Ping", "Query", "QueryRange"}) {
		t.Errorf("methods = %v: a new method needs a decision in the spec", names)
	}
}

func TestACAFileThatIsNotACertificateIsRefused(t *testing.T) {
	f := t.TempDir() + "/ca.pem"
	if err := os.WriteFile(f, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := metrics.New(creds("http://x", ""), f); err == nil {
		t.Fatal("a CA file without a certificate was accepted")
	}
	if _, err := metrics.New(creds("http://x", ""), t.TempDir()+"/missing.pem"); err == nil {
		t.Fatal("a missing CA file was accepted")
	}
}

// limitBody builds a matrix answer whose first 4 MiB end exactly after the closing brace of a series, followed by more series.
func limitBody(t *testing.T) (body string, wholeSeries int) {
	t.Helper()
	const limit = 4 << 20
	head := `{"status":"success","data":{"resultType":"matrix","result":[`
	entry := func(i, pad int) string {
		return fmt.Sprintf(`{"metric":{"instance":"host-%06d","pad":"%s"},"values":[[1700000000,"1"]]}`, i, strings.Repeat("p", pad))
	}
	var parts []string
	size := len(head)
	for i := 0; ; i++ {
		e := entry(i, 0)
		sep := 0
		if i > 0 {
			sep = 1
		}
		if size+sep+len(e) > limit {
			break
		}
		parts = append(parts, e)
		size += sep + len(e)
	}
	slack := limit - size
	parts[0] = entry(0, slack) // the slack goes into the first series, so the last one ends on the limit
	cut := head + strings.Join(parts, ",")
	if len(cut) != limit {
		t.Fatalf("the cut is %d bytes, want %d", len(cut), limit)
	}
	return cut + "," + entry(len(parts), 0) + `]}}`, len(parts)
}

func TestAnAnswerCutExactlyAfterASeriesAtTheByteLimitIsTruncated(t *testing.T) {
	body, whole := limitBody(t)
	fake := metricstest.New(t, "")
	fake.SetRaw(200, body)
	start := time.Unix(1700000000, 0)
	got, err := newClient(t, creds(fake.URL, "")).QueryRange(ctx, "up", start, start.Add(time.Hour), time.Minute)
	if err != nil || !got.Truncated || len(got.Series) != whole {
		t.Fatalf("%d series (want %d), truncated = %v, %v", len(got.Series), whole, got.Truncated, err)
	}
}

func TestAnAnswerThatStopsAfterASeriesWithoutHittingTheLimitIsNotAResult(t *testing.T) {
	fake := metricstest.New(t, "")
	for name, body := range map[string]string{
		"after a series":    `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"2"]}`,
		"after a comma":     `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"2"]},`,
		"after the opening": `{"status":"success","data":{"resultType":"vector","result":[`,
	} {
		fake.SetRaw(200, body)
		got, err := newClient(t, creds(fake.URL, "")).Query(ctx, "up", time.Time{})
		if !errors.Is(err, metrics.ErrNotMetrics) || got.Series != nil || got.Truncated {
			t.Errorf("%s: %+v, %v", name, got, err)
		}
	}
}

func TestAnErrorAnswerWithoutAMessageHasAFixedText(t *testing.T) {
	fake := metricstest.New(t, "")
	for _, body := range []string{`{"status":"error"}`, `{"status":"error","errorType":"x","error":""}`, `{"status":"error","error":"  \n "}`} {
		fake.SetRaw(400, body)
		_, err := newClient(t, creds(fake.URL, "")).Query(ctx, "up", time.Time{})
		if err == nil || err.Error() != "metrics: the query was refused: the store gave no reason" {
			t.Errorf("%s: %v", body, err)
		}
	}
}
