package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/testutil"
)

// A delete that keeps finding its rows locked (store.ErrDatabaseBusy) answers 503 with a short sentence and Retry-After,
// not a 500, and deletes nothing; once the lock is gone the same request goes through.
func TestABusyDeleteAnswers503WithRetryAfter(t *testing.T) {
	st, dsn := testutil.StoreDSN(t)
	ts := httptest.NewServer(server.New(server.Deps{Store: st, Auth: auth.New(password), RunnerToken: runnerToken}))
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	do := func(method, path, body string) (*http.Response, string) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("X-Remedy-CSRF", "1")
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	if resp, _ := do(http.MethodPost, "/api/login", `{"password":"`+password+`"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	resp, body := do(http.MethodPost, "/api/skills", `{"name":"lab-notes","description":"d","body":"b"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d %s", resp.StatusCode, body)
	}
	var sk struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &sk); err != nil {
		t.Fatal(err)
	}

	// Another session holds the skill's row for the whole of the delete's attempts.
	ctx := context.Background()
	raw := testutil.RawDB(t, dsn)
	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }() // also on a failure, so the schema can be dropped
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM skills WHERE id = $1 FOR UPDATE`, sk.ID); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/skills/%d", sk.ID)
	resp, body = do(http.MethodDelete, path, "")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("busy delete = %d %s, want 503", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Retry-After"); got != "2" {
		t.Errorf("Retry-After = %q, want 2", got)
	}
	if !strings.Contains(body, `"the database is busy; try again"`) {
		t.Errorf("body = %s", body)
	}
	_ = tx.Rollback()

	if resp, body := do(http.MethodDelete, path, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete after the lock = %d %s", resp.StatusCode, body)
	}
}
