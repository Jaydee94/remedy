#!/bin/sh
# Spike: does `claude -p --json-schema` deliver structured output in stream-json mode?
# Uses the same isolation flags as the Remedy runner. Prints, per output format, where the
# structured answer shows up.
set -eu

OUT=$(mktemp -d)
SCHEMA='{"type":"object","properties":{"summary":{"type":"string"},"confidence":{"type":"string","enum":["high","medium","low"]}},"required":["summary","confidence"],"additionalProperties":false}'
PROMPT="A CI build failed with: cannot find module left-pad. Give a one sentence summary and your confidence."

run() {
  label=$1
  shift
  env -u ANTHROPIC_API_KEY -u ANTHROPIC_AUTH_TOKEN \
    claude -p "$PROMPT" --permission-mode dontAsk \
    --safe-mode --restricted --strict-mcp-config --tools "Read" --model sonnet \
    --json-schema "$SCHEMA" "$@" > "$OUT/$label.out" 2> "$OUT/$label.err" || echo "$label: exit $?"
}

run stream --output-format stream-json --verbose
run json --output-format json

echo "== stream-json: result event"
jq -c 'select(.type=="result") | {subtype, is_error, has_structured_output: (has("structured_output")), structured_output, result}' "$OUT/stream.out"
echo "== json: result object"
jq -c '{subtype, is_error, has_structured_output: (has("structured_output")), structured_output, result}' "$OUT/json.out"
echo "raw output kept in $OUT"
