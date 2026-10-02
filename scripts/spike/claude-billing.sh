#!/bin/sh
# Spike helper: one real `claude -p` call on the subscription login.
# Strips API-key variables so the subscription is the only possible credential.
set -eu

out=$(env -u ANTHROPIC_API_KEY -u ANTHROPIC_AUTH_TOKEN \
  claude -p "Reply with the single word: pong" \
    --output-format json \
    --permission-mode dontAsk)

echo "$out" | jq '{result, session_id, total_cost_usd, usage}'
