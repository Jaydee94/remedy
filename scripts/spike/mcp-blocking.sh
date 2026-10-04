#!/bin/bash
# Spike: how does the `claude` CLI behave when an MCP tool blocks for a long time (an approval wait)?
#
#   scripts/spike/mcp-blocking.sh <mode> <seconds> [NAME=value ...]
#
# mode json: the tool answer is a plain JSON response after the delay.
# mode sse:  a text/event-stream, with a comment line as keepalive every KEEPALIVE seconds (default 20).
# mode ssep: a text/event-stream with MCP progress notifications every KEEPALIVE seconds.
#
# Starts mcp_blocking.py on a free port, runs the CLI with the isolation flags of the Remedy runner minus --safe-mode
# (which turns MCP off, see docs/research/spike-mcp-blocking.md) and that server as the only MCP server, and prints
# what the server and the CLI saw. Extra NAME=value arguments are set in the CLI's environment, for example
# MCP_TOOL_TIMEOUT=1200000 or CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT=0.
set -u
DIR=$(cd "$(dirname "$0")" && pwd)
MODE=${1:?mode json|sse|ssep}
SECS=${2:?seconds}
shift 2
WORK=$(mktemp -d)
PORT=$((20000 + RANDOM % 20000))

python3 "$DIR/mcp_blocking.py" "$PORT" "$MODE" > "$WORK/server.log" 2>&1 &
SERVER=$!
trap 'kill $SERVER 2>/dev/null; rm -rf "$WORK"' EXIT
sleep 1
cat > "$WORK/mcp.json" <<EOF
{"mcpServers":{"spike":{"type":"http","url":"http://127.0.0.1:$PORT/mcp","headers":{"Authorization":"Bearer run-token-spike"}}}}
EOF

START=$(date +%s)
cd "$WORK" || exit 1
env -u ANTHROPIC_API_KEY -u ANTHROPIC_AUTH_TOKEN "$@" \
  claude -p "Call the wait tool of the spike server with seconds=$SECS, then reply with exactly: done" \
  --permission-mode dontAsk --restricted --strict-mcp-config \
  --mcp-config "$WORK/mcp.json" --tools "Read" --allowedTools "mcp__spike__wait" --model sonnet \
  --output-format stream-json --verbose > "$WORK/out" 2> "$WORK/err"
RC=$?
echo "== mode=$MODE seconds=$SECS exit=$RC took=$(($(date +%s) - START))s"
echo "-- server log:"
cat "$WORK/server.log"
echo "-- events:"
jq -c 'select(.type=="system" and .subtype=="init") | {mcp_servers, tools}' "$WORK/out"
jq -c 'select(.type=="assistant") | .message.content[]? | select(.type=="tool_use") | {tool_use: .name, input}' "$WORK/out"
jq -c 'select(.type=="user") | .message.content[]? | select(.type=="tool_result") | {tool_result: .content, is_error}' "$WORK/out"
jq -c 'select(.type=="result") | {subtype, is_error, result, num_turns, permission_denials, total_cost_usd}' "$WORK/out"
head -c 600 "$WORK/err"
