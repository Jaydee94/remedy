package runner_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/testutil"
)

// record reads what RecordingClaude wrote: one key=value per line.
func record(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("the agent recorded nothing: %v", err)
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			out[k] = v
		}
	}
	return out
}

func TestARunWithToolsGetsItsConfigInAFileTheAgentCannotReach(t *testing.T) {
	recorded := filepath.Join(t.TempDir(), "record.txt")
	st, ts := startLoop(t, map[string]provider.Provider{"claude": provider.Claude{Binary: testutil.RecordingClaude(t, recorded)}})
	queued, err := st.CreateToolRun(context.Background(), "claude", "use the tools")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitTerminal(t, st, queued.ID); got.Status != run.Succeeded {
		t.Fatalf("run = %+v", got)
	}

	rec := record(t, recorded)
	args := " " + rec["args"] + " "
	if strings.Contains(args, " --safe-mode ") || !strings.Contains(args, " --mcp-config ") || !strings.Contains(args, " --allowedTools mcp__remedy ") {
		t.Fatalf("args = %q", rec["args"])
	}
	if rec["mode"] != "600" || rec["dirmode"] != "700" {
		t.Fatalf("the config has mode %q in a directory of mode %q, want 600 and 700", rec["mode"], rec["dirmode"])
	}

	var cfg struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(rec["body"]), &cfg); err != nil {
		t.Fatalf("the config is not JSON: %v\n%s", err, rec["body"])
	}
	server, ok := cfg.MCPServers["remedy"]
	if !ok || len(cfg.MCPServers) != 1 || server.Type != "http" || server.URL != ts.URL+"/mcp" {
		t.Fatalf("config = %+v, want one http server at %s/mcp", cfg, ts.URL)
	}
	bearer, found := strings.CutPrefix(server.Headers["Authorization"], "Bearer ")
	if !found || len(bearer) != 43 {
		t.Fatalf("Authorization = %q, want a bearer token of 43 characters", server.Headers["Authorization"])
	}

	// The file is outside the workspace of the agent, and gone when the run has ended.
	cfgPath := rec["cfg"]
	if filepath.Dir(cfgPath) == rec["cwd"] || strings.HasPrefix(cfgPath, rec["cwd"]+string(filepath.Separator)) ||
		filepath.Base(filepath.Dir(cfgPath)) == filepath.Base(rec["cwd"]) {
		t.Fatalf("the config %s is inside the workspace %s", cfgPath, rec["cwd"])
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("the config still exists after the run: %v", err)
	}

	// The token is nowhere in what the run reported.
	events, _ := st.Events(context.Background(), queued.ID, 0)
	for _, e := range events {
		if strings.Contains(string(e.Payload), bearer) {
			t.Fatalf("an event of the run contains the token: %s", e.Payload)
		}
	}
}

func TestARunWithoutToolsHasNoConfigAndKeepsSafeMode(t *testing.T) {
	recorded := filepath.Join(t.TempDir(), "record.txt")
	st, _ := startLoop(t, map[string]provider.Provider{"claude": provider.Claude{Binary: testutil.RecordingClaude(t, recorded)}})
	queued, _ := st.CreateRun(context.Background(), "claude", "plain")
	if got := waitTerminal(t, st, queued.ID); got.Status != run.Succeeded {
		t.Fatalf("run = %+v", got)
	}

	rec := record(t, recorded)
	args := " " + rec["args"] + " "
	if !strings.Contains(args, " --safe-mode ") || strings.Contains(args, " --mcp-config ") || rec["cfg"] != "" {
		t.Fatalf("args = %q, cfg = %q", rec["args"], rec["cfg"])
	}
}

// The config holds the run's token, and the token dies when the run is reported finished. The file must be gone before that
// report is made: removing it afterwards (a deferred cleanup that ran after the report) left a window in which the run was
// over for everyone else and the file still existed, and made TestARunWithToolsGetsItsConfigInAFileTheAgentCannotReach flaky.
func TestTheConfigIsGoneBeforeTheRunIsReportedFinished(t *testing.T) {
	recorded := filepath.Join(t.TempDir(), "record.txt")
	var mu sync.Mutex
	var leftAtFinish []string
	st, _ := startLoopObserving(t, map[string]provider.Provider{"claude": provider.Claude{Binary: testutil.RecordingClaude(t, recorded)}}, 0,
		func(root string, r *http.Request) {
			if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/finish") {
				return
			}
			entries, _ := os.ReadDir(root)
			mu.Lock()
			defer mu.Unlock()
			for _, e := range entries {
				if strings.Contains(e.Name(), "-mcp-") {
					leftAtFinish = append(leftAtFinish, e.Name())
				}
			}
		})
	queued, err := st.CreateToolRun(context.Background(), "claude", "use the tools")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitTerminal(t, st, queued.ID); got.Status != run.Succeeded {
		t.Fatalf("run = %+v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(leftAtFinish) > 0 {
		t.Fatalf("the MCP config directory %v still existed when the runner reported the run finished", leftAtFinish)
	}
}
