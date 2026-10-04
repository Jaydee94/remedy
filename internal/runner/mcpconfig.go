package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/Jaydee94/remedy/internal/provider"
)

// writeMCPConfig writes the MCP config of a run with gatekeeper access: where the gatekeeper is and the run token
// that opens it. The file holds a secret, so it is made with mode 0600 in a directory of its own (mode 0700) next to
// the workspace of the run, not inside it: the file tools of the agent reach the workspace only, and the run token
// must not become part of what the agent reads and sends on. cleanup removes the directory.
func writeMCPConfig(root, runID, baseURL, token string) (path string, cleanup func(), err error) {
	dir, err := os.MkdirTemp(root, runID+"-mcp-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	cfg := map[string]any{"mcpServers": map[string]any{
		provider.MCPServerName: map[string]any{
			"type":    "http",
			"url":     strings.TrimRight(baseURL, "/") + "/mcp",
			"headers": map[string]string{"Authorization": "Bearer " + token},
		},
	}}
	body, err := json.Marshal(cfg)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	path = filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}
