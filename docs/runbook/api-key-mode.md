# Runbook: running Remedy on an API key

**Status.** Implemented in cycle 7b (plan [`docs/plans/self-hosted-7b-api-key-mode.md`](../plans/self-hosted-7b-api-key-mode.md), spec section 5 of
[`docs/specs/2026-10-21-self-hosted-for-others-design.md`](../specs/2026-10-21-self-hosted-for-others-design.md)). Tested with a fake CLI only: **no real run with
a key has been made**, so what the CLI really reports with a key is unverified (section 8). Do not call it proven.

By default the agent CLI bills your subscription login (you log in once, from the Setup page). In this mode it bills an Anthropic API key instead,
by the token, on the account of the key. Pick it if you are unsure whether a subscription login may drive a tool like this, if you run Remedy around the
clock, or if you would rather have a spending limit that is a number than a quota. Read the vendor's current terms before you decide
(`docs/research/subscription-cli-usage.md` is the maintainer's reading of 2026-10-02, not legal advice).

The mode is chosen once, at install time, by the chart. It is not a switch on the Setup page: a click must not change who is billed.

## 1. The Secret

Create the key in the Anthropic Console, ideally with a spending limit of its own, and put it in a Secret in the Remedy namespace. Never write it on a command
line or into a values file; read it silently:

```sh
read -rs ANTHROPIC_KEY    # paste the key, press Enter
printf 'api-key=%s\n' "$ANTHROPIC_KEY" | kubectl -n remedy create secret generic anthropic-key --from-env-file=/dev/stdin
unset ANTHROPIC_KEY
```

## 2. The values

```yaml
runner:
  auth:
    mode: apiKey
    apiKeySecret:
      name: anthropic-key   # the Secret of section 1
      key: api-key          # its key
```

Then `helm upgrade` (or sync). The chart gives the key to the runner container only (as `ANTHROPIC_API_KEY`, with `REMEDY_AUTH_MODE=api_key`); no other
container and no other template references the Secret, and the control plane never receives the key. The chart refuses a mode other than `subscription` or
`apiKey`, a missing Secret name or key, and a key given as a value.

**Rollout order.** The runner reports its mode in its status, and the control plane decodes that body strictly. A new runner that reports to an older control plane is
answered 400 until the control plane is upgraded (it retries every 15 seconds). The chart upgrades both together; if you upgrade by hand, upgrade the control plane first.

## 3. What you see

On Setup, "My runner" shows **Billed to your API key (Console)** and **Key present**: the page does not look at the key at all: it shows "Key present" whenever the runner reports key mode. The runner itself refuses to start (exit 2) without a key of the right shape (20 or more letters, digits, `-` or `_`, with no space or line break around it; a Secret made with `--from-file` from a file that ends in a newline is refused for that), and a key that is well-formed but not valid fails the first run with a fixed sentence. There is no "Log in from here" in this mode.

A run says **billed to your API key, about $0.12 (the CLI's estimate)**; the figure is the CLI's own estimate, not a charge, and your invoice decides. A subscription run shows no money
anywhere, because for a subscription the figure means nothing. Under "My limits" you see **About $1.20 of $5.00 today**.

## 4. The budgets

Two settings on the Setup page (and `REMEDY_API_BUDGET_PER_RUN_CENTS`, `REMEDY_API_BUDGET_PER_DAY_CENTS`), in whole cents:

- **Most one run may cost** (5 to 2000, default 100): the runner passes it to the CLI as `--max-budget-usd`. A runner in key mode with no value from the control plane uses 1.00 itself.
  What the CLI does when a run reaches it is unverified (section 8): the run is recorded as it ends.
- **Most automatic runs may cost per 24 hours** (0 to 100000, default 500): an automatic diagnosis is refused, with the reason in the thread, when the key-billed runs of the last 24 hours
  cost this much or more. The sum is the CLI's estimate per finished run; a run that was killed before the CLI printed its cost (timeout, cancel, lost runner) counts at the per-run budget it was allowed. `0` turns automatic runs off. A run you start yourself is never refused for it. It is enforced unless the runner has reported subscription mode, so also during the first seconds after a restart of the control plane.

The existing limits on the number of diagnoses stay. Set a spending limit on the key in the Console as well: Remedy's budgets rest on the CLI's estimate.

## 5. The guard

Each run says how it will be billed in its own first event. The runner stops a run as soon as it has read that event, before the event is posted, when it contradicts its mode (one request may already be on its way; the window cannot be closed entirely): a key where the subscription is configured (a leftover variable, a stored
login that wins), or the subscription where a key is configured. In key mode a run whose event says nothing is stopped too: so is a run that goes on without any init event, or whose init event cannot be read, and one that ends well without one. The run fails with the reason "billing_mismatch" and the
sentence "The run was stopped: it would have been billed differently than configured."; nothing it wrote afterwards is kept. In subscription mode the guard accepts only the source `none`, which was measured for an interactive login; a `CLAUDE_CODE_OAUTH_TOKEN` login (`setup-token`) is untested, and if the CLI reports anything else for it every run is stopped with "billing_mismatch" (the real run in section 8 must cover it). A runner in subscription mode that finds `ANTHROPIC_API_KEY` in its environment
ignores it and says so once in its log, without the value.

## 6. What happens to the key

It lives in the environment of the runner container: anyone who can `kubectl exec` into the runner pod can read it, exactly as they could read the subscription login there. The runner scrubs the
literal key (and its first and last six characters where they stand alone) from every event, from stderr and from the result it posts, replacing it with `[REDACTED:api-key]`; `internal/redact` also
masks the vendor's key format in text that reaches a prompt. A key that leaks anyway is revoked in the Console; a new Secret and a restart of the runner pod bring the new one in.

## 7. Back to the subscription

Set `runner.auth.mode: subscription` (and remove `apiKeySecret`), upgrade, then log in from the Setup page. The runs recorded as billed to the key keep that record.

## 8. Not verified, and what the first real run must settle

A real run with a key costs money on your key, so it is yours to start. Use a key with a low spending limit. It has to answer (spec section 5.1): which value the first event carries for a key
(the guard accepts any value other than "none"); whether the key wins over a stored subscription login without `--bare`; what a run looks like when `--max-budget-usd` is reached; what the CLI's login
check says with only a key (the page ignores it in this mode); the current key format (`sk-ant-`, 20 or more characters is assumed by the redaction); and whether the scrub catches what the CLI prints for a rejected key; which source the CLI reports for a `setup-token` login (subscription mode).
Write the result to `docs/research/api-key-mode-real-run.md` as shapes, never a key and never a whole event, and fix the guard first if a result contradicts it.
