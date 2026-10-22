package gatekeeper

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Only the tool's own deadline is a timeout. A run that was cancelled (the client went away, the run ended) is not told that the source
// was slow.
func TestOnlyTheDeadlineOfTheToolIsReportedAsATimeout(t *testing.T) {
	failure := errors.New("Get \"http://127.0.0.1:1/api\": context canceled")

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for name, err := range map[string]error{"metrics": metricsErr(cancelled, failure), "alertmanager": alertErr(cancelled, failure)} {
		if msg := toolErrorMessage(err); strings.Contains(msg, "10 seconds") {
			t.Errorf("%s: a cancelled context is reported as a timeout: %q", name, msg)
		}
	}

	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for name, err := range map[string]error{"metrics": metricsErr(expired, failure), "alertmanager": alertErr(expired, failure)} {
		if msg := toolErrorMessage(err); !strings.Contains(msg, "did not answer within 10 seconds") {
			t.Errorf("%s: an expired deadline = %q, want the timeout sentence", name, msg)
		}
	}
}
