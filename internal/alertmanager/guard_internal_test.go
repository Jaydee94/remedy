package alertmanager

import (
	"net/http"
	"strings"
	"testing"
)

func TestTheTransportRefusesEveryMethodButGetAndHead(t *testing.T) {
	cl, err := New(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		req, _ := http.NewRequest(m, "http://127.0.0.1:1/api/v2/alerts", nil)
		if _, err := cl.http.Do(req); err == nil || !strings.Contains(err.Error(), "refusing to send") {
			t.Errorf("%s: error = %v", m, err)
		}
	}
}
