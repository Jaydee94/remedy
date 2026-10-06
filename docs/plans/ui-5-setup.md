# UI redesign, part 5: Setup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the settings page (the GitHub connection card, the repositories card, the limits card) by "Setup": one column of Remedy's own words, with the same actions, and put "Sign out" at the end of the page on a phone.

**Architecture:** What the page says is built by pure, tested functions (`setup.ts`: the limits paragraph, the diagnosis bar, the repository check and sub line, the connection chip). Three sections are small components (`GitHubSection`, `ReposSection`, `LimitsSection`) over the existing API; the page owns the connection state. The shell context gets a `signOut` so a page can offer it. The last route leaves `LegacyPage`, which is deleted.

**Tech Stack:** React 19, React Router 8, TypeScript 7, Tailwind 4, `node --test`.

**Spec:** [`docs/specs/2026-10-05-ui-conversation-redesign-design.md`](../specs/2026-10-05-ui-conversation-redesign-design.md), section 8 (and 2, 3.1, 3.2, 9, 10). Earlier plans: `ui-1-foundation.md` to `ui-4-needs-you-and-ask.md`. The backend it needs (`diagnosesLast24h` on `GET /api/limits`) is merged (part 0).

## Global Constraints

- Everything in the repo is English: code, comments, UI copy, docs, commit messages.
- `web/tsconfig.app.json` sets `erasableSyntaxOnly` and `verbatimModuleSyntax`: no enums or constructor parameter properties, `import type` for types.
- Pure logic modules (`setup.ts`, `incidents.ts`) import only with relative paths and `.ts` extensions and contain only erasable TypeScript. Components may use the `@/` alias.
- **The GitHub token is write-only.** The page shows only the hint (the last four characters) the API returns; the token field is `type="password"`, `autoComplete="off"`, is cleared after a request and never echoed, logged or stored in the browser (no `localStorage`).
- **Untrusted text** (`statusDetail`, `lastError`, repository names, the login) is rendered as React text only: never as HTML. A sentence's structure never comes from such text: Remedy's sentences are templates filled with values.
- Destructive actions keep `ConfirmButton` (Disconnect, Remove); a switch acts at once.
- Palette and tokens of part 1; no new hard-coded palette colours (`bg-rose-500` and the like). Form fields are 16 px on a phone (`text-base md:text-sm`) so that iOS does not zoom.
- Routes do not change (`/settings` stays the route of Setup).
- Commits end with the line `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>` (exactly this model name).
- In a worktree-isolated session the Bash tool refuses commands that name git in a compound or variable form: use plain separate commands or a script file with literal paths (see `CLAUDE.md`); run npm with `--prefix web`. A fresh worktree has no `web/node_modules`: run `npm ci --prefix web` first. Use `command ls`. `npm run lint` must print no warnings: a `setState` inside an effect body and `Date.now()` in render are lint warnings in this project.
- Work on a branch `feat/ui-5-setup`, never on `main`.

## Review Focus

1. The token never appears: not in the DOM after a request, not in an error message, not in `title`/`aria` attributes; Connect and Replace token are disabled for an empty or whitespace token; a refusal of the server shows in place and the page is not left half changed. (Tasks 3, 4.)
2. A connection with an error status or `undecryptable` shows the chip in colour and `statusDetail` as an error card, and the actions still work (Check connection, Replace token, Disconnect). (Task 3.)
3. A repository whose `lastError` is long, or whose name is long, breaks inside the row at 390 px; the switch and Remove stay reachable. (Tasks 2, 4.)
4. Adding a repository: a wrong form ("homelab", "a/b/c", spaces, an empty string) is refused in the browser with the fixed sentence before any request; the API's own refusal (not found, already added) shows in place; a repository name with odd characters from the server is only text. (Task 1 tests, Task 2.)
5. Limits: `diagnoseMaxPerDay` 0 reads "I don't diagnose on my own; you can start it by hand." and shows no bar; a limit of 1 reads "1 time"; the bar caps at 100 % when the count is above the limit and is announced as a progress bar; a failing limits request shows an error and does not hide the rest of the page. (Task 1 tests, Task 2.)
6. On a phone "Sign out" is the last element of the page, also while the page is loading or after a load error; on a desktop it is not repeated (the sidebar has it); it signs out. (Tasks 3, 4.)
7. 390 px: nothing scrolls sideways; the page has one `h1`; every control has a focus ring; the switch and the buttons are at least 40 px high. (Tasks 2 to 4.)
8. Not connected: "Connect GitHub first." in the repositories section, no request for the list. (Task 2.)

---

## File Structure

| File | Change | Responsibility |
|---|---|---|
| `web/src/api.ts` | modify | `Limits.diagnosesLast24h` |
| `web/src/setup.ts` + `setup.test.ts` | create | `duration`, `everyText`, `limitsText`, `diagnosisBar`, `validRepoName`, `repoSubline`, `connectionView` |
| `web/src/components/setup/LimitsSection.tsx` | create | "My limits" |
| `web/src/components/setup/ReposSection.tsx` | create | "Repositories I watch" |
| `web/src/components/setup/GitHubSection.tsx` | create | "GitHub" |
| `web/src/SetupPage.tsx` | create | the page (replaces `SettingsPage.tsx`, `GitHubConnectionCard.tsx`, `ReposCard.tsx`, `LimitsCard.tsx`, deleted) |
| `web/src/shellContext.ts`, `web/src/components/AppLayout.tsx` | modify | `signOut` in the shell context, `useSignOut()` |
| `web/src/App.tsx` | modify | the `settings` route and the catch-all leave `LegacyPage` |
| `web/src/components/LegacyPage.tsx` | delete | no page is left in it |
| `README.md`, `docs/runbook/first-real-run.md` | modify | **Settings** is **Setup** |

---

### Task 1: The logic (setup.ts) and the limits type

**Files:**
- Create: `web/src/setup.ts`, `web/src/setup.test.ts`
- Modify: `web/src/api.ts`

**Interfaces:**
- Consumes: `Limits`, `Repo`, `GitHubConnection` from `api.ts`; `timeAgo(iso, now?)` from `incidents.ts`.
- Produces: `duration(seconds)`, `everyText(seconds)`, `limitsText(limits)`, `diagnosisBar(limits)`, `validRepoName(name)`, `repoSubline(repo, now?)`, `connectionView(status)`.

- [ ] **Step 1: The limits type**

In `web/src/api.ts` add to `Limits`:

```ts
  /** Automatic diagnoses started in the last 24 hours (they count against `diagnoseMaxPerDay`). */
  diagnosesLast24h: number
```

(Check `internal/server/responder.go`, `limitsView`, for the JSON name `diagnosesLast24h`.)

- [ ] **Step 2: Write the failing tests**

`web/src/setup.test.ts`:

```ts
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { Limits, Repo } from './api.ts'
import { connectionView, diagnosisBar, duration, everyText, limitsText, repoSubline, validRepoName } from './setup.ts'

const limits = (over: Partial<Limits> = {}): Limits => ({
  pollIntervalSeconds: 60,
  diagnoseCooldownSeconds: 900,
  diagnoseMaxPerIncident: 3,
  diagnoseMaxPerDay: 20,
  staleRunMinutes: 15,
  diagnosesLast24h: 4,
  ...over,
})
const repo = (over: Partial<Repo> = {}): Repo => ({ id: 1, fullName: 'octo/homelab', defaultBranch: 'main', enabled: true, lastError: '', createdAt: '2026-10-05T07:00:00Z', ...over })

describe('duration and everyText', () => {
  it('names the largest whole unit', () => {
    assert.equal(duration(30), '30 seconds')
    assert.equal(duration(60), '1 minute')
    assert.equal(duration(900), '15 minutes')
    assert.equal(duration(90), '90 seconds')
    assert.equal(duration(3600), '1 hour')
    assert.equal(duration(7200), '2 hours')
    assert.equal(duration(5400), '90 minutes')
  })
  it('says "every minute", not "every 1 minute"', () => {
    assert.equal(everyText(60), 'minute')
    assert.equal(everyText(300), '5 minutes')
    assert.equal(everyText(3600), 'hour')
    assert.equal(everyText(10), '10 seconds')
  })
})

describe('limitsText', () => {
  it('builds the paragraph of the spec from the limits', () => {
    assert.equal(
      limitsText(limits()),
      'I check every minute. I diagnose up to 3 times per incident and 20 times per 24 hours, at least 15 minutes apart, one run at a time. A run that stays running for 15 minutes is failed.',
    )
  })
  it('says that it does not diagnose on its own when the daily limit is 0', () => {
    const text = limitsText(limits({ diagnoseMaxPerDay: 0 }))
    assert.ok(text.includes("I don't diagnose on my own; you can start it by hand."))
    assert.ok(!text.includes('per incident'))
  })
  it('uses the singular for a limit of 1', () => {
    const text = limitsText(limits({ diagnoseMaxPerIncident: 1, diagnoseMaxPerDay: 1, staleRunMinutes: 1 }))
    assert.ok(text.includes('up to 1 time per incident and 1 time per 24 hours'))
    assert.ok(text.endsWith('A run that stays running for 1 minute is failed.'))
  })
})

describe('diagnosisBar', () => {
  it('is absent when automatic diagnosis is off', () => {
    assert.equal(diagnosisBar(limits({ diagnoseMaxPerDay: 0 })), null)
  })
  it('gives the count, the limit and the share', () => {
    assert.deepEqual(diagnosisBar(limits({ diagnosesLast24h: 5, diagnoseMaxPerDay: 20 })), { used: 5, max: 20, ratio: 0.25, full: false })
  })
  it('caps the share at 100 percent and marks a full bar', () => {
    assert.deepEqual(diagnosisBar(limits({ diagnosesLast24h: 25, diagnoseMaxPerDay: 20 })), { used: 25, max: 20, ratio: 1, full: true })
    assert.equal(diagnosisBar(limits({ diagnosesLast24h: 20, diagnoseMaxPerDay: 20 }))?.full, true)
  })
  it('treats a missing count as 0', () => {
    const l = { ...limits(), diagnosesLast24h: undefined } as unknown as Limits
    assert.deepEqual(diagnosisBar(l), { used: 0, max: 20, ratio: 0, full: false })
  })
})

describe('validRepoName', () => {
  it('accepts owner/name', () => {
    for (const ok of ['jaydee94/homelab', 'octo/remedy.git', 'a/b', ' octo/homelab ', 'my-org/my_repo.v2']) assert.equal(validRepoName(ok), true, ok)
  })
  it('refuses everything else', () => {
    for (const bad of ['', '   ', 'homelab', 'a/b/c', 'a/', '/b', 'a b/c', 'octo/home lab', 'octo//homelab', 'octo/<b>x</b>', 'https://github.com/octo/homelab']) {
      assert.equal(validRepoName(bad), false, bad)
    }
  })
})

describe('repoSubline', () => {
  const now = Date.parse('2026-10-05T10:00:00Z')
  it('names the branch and the last poll', () => {
    assert.equal(repoSubline(repo({ lastPolledAt: '2026-10-05T09:57:00Z' }), now), 'main, polled 3 min ago')
  })
  it('says never polled', () => {
    assert.equal(repoSubline(repo(), now), 'main, never polled')
  })
  it('says paused when the repository is off', () => {
    assert.equal(repoSubline(repo({ enabled: false, lastPolledAt: '2026-10-05T09:57:00Z' }), now), 'main, polled 3 min ago · paused')
    assert.equal(repoSubline(repo({ enabled: false }), now), 'main, never polled · paused')
  })
})

describe('connectionView', () => {
  it('has a label and token colours for every status', () => {
    assert.equal(connectionView('ok').label, 'Connected')
    assert.equal(connectionView('error').label, 'Error')
    assert.equal(connectionView('undecryptable').label, 'Cannot decrypt')
    for (const s of ['ok', 'error', 'undecryptable'] as const) {
      const v = connectionView(s)
      assert.match(v.dot, /^bg-(success|destructive)$/)
      assert.match(v.soft, /^bg-soft-(resolved|open)$/)
      assert.match(v.text, /^text-(success|destructive)$/)
    }
  })
})
```

- [ ] **Step 3: Run the tests to see them fail**

Run: `npm test --prefix web`. Expected: FAIL (module missing).

- [ ] **Step 4: Write the logic**

`web/src/setup.ts`:

```ts
import type { GitHubConnection, Limits, Repo } from './api.ts'
import { timeAgo } from './incidents.ts'

const unit = (n: number, name: string) => `${n} ${name}${n === 1 ? '' : 's'}`

/** The largest unit that divides `seconds` evenly: 90 seconds stay 90 seconds, 5400 seconds are 90 minutes. */
function largest(seconds: number): { n: number; name: string } {
  if (seconds < 60 || seconds % 60 !== 0) return { n: seconds, name: 'second' }
  if (seconds < 3600 || seconds % 3600 !== 0) return { n: seconds / 60, name: 'minute' }
  return { n: seconds / 3600, name: 'hour' }
}

/** "15 minutes". */
export function duration(seconds: number): string {
  const { n, name } = largest(seconds)
  return unit(n, name)
}

/** What follows "every": "minute", "5 minutes". */
export function everyText(seconds: number): string {
  const { n, name } = largest(seconds)
  return n === 1 ? name : unit(n, name)
}

const times = (n: number) => (n === 1 ? '1 time' : `${n} times`)

/** What Remedy does on its own, as a paragraph in Remedy's voice, from the limits the server answers. */
export function limitsText(l: Limits): string {
  const poll = `I check every ${everyText(l.pollIntervalSeconds)}.`
  const diagnose =
    l.diagnoseMaxPerDay === 0
      ? "I don't diagnose on my own; you can start it by hand."
      : `I diagnose up to ${times(l.diagnoseMaxPerIncident)} per incident and ${times(l.diagnoseMaxPerDay)} per 24 hours, at least ${duration(l.diagnoseCooldownSeconds)} apart, one run at a time.`
  const stale = `A run that stays running for ${unit(l.staleRunMinutes, 'minute')} is failed.`
  return [poll, diagnose, stale].join(' ')
}

/** The bar of the automatic diagnoses of the last 24 hours, or null when automatic diagnosis is off. */
export function diagnosisBar(l: Limits): { used: number; max: number; ratio: number; full: boolean } | null {
  if (l.diagnoseMaxPerDay <= 0) return null
  const used = l.diagnosesLast24h ?? 0
  return { used, max: l.diagnoseMaxPerDay, ratio: Math.min(1, used / l.diagnoseMaxPerDay), full: used >= l.diagnoseMaxPerDay }
}

/** "owner/name": one slash, two parts of letters, digits, dot, dash and underscore. The API still has the last word. */
export function validRepoName(name: string): boolean {
  return /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(name.trim())
}

/** The line under a repository's name: its branch, the last poll, and "paused" when it is off. */
export function repoSubline(repo: Repo, now: number = Date.now()): string {
  const polled = repo.lastPolledAt ? `polled ${timeAgo(repo.lastPolledAt, now)}` : 'never polled'
  return `${repo.defaultBranch}, ${polled}${repo.enabled ? '' : ' · paused'}`
}

/** The chip of the connection's status, in tokens. */
export function connectionView(status: NonNullable<GitHubConnection['status']>): { label: string; dot: string; soft: string; text: string } {
  switch (status) {
    case 'ok':
      return { label: 'Connected', dot: 'bg-success', soft: 'bg-soft-resolved', text: 'text-success' }
    case 'error':
      return { label: 'Error', dot: 'bg-destructive', soft: 'bg-soft-open', text: 'text-destructive' }
    case 'undecryptable':
      return { label: 'Cannot decrypt', dot: 'bg-destructive', soft: 'bg-soft-open', text: 'text-destructive' }
  }
}
```

- [ ] **Step 5: Run the tests, lint and build**

Run: `npm test --prefix web`, `npm run lint --prefix web`, `npm run build --prefix web`. Expected: all pass, lint prints nothing.

- [ ] **Step 6: Commit**

```sh
git add web/src/setup.ts web/src/setup.test.ts web/src/api.ts
git commit -m "feat(web): the logic of Setup" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: The limits and repositories sections

**Files:**
- Create: `web/src/components/setup/LimitsSection.tsx`, `web/src/components/setup/ReposSection.tsx`

**Interfaces:**
- Consumes: Task 1; `api.getLimits`, `api.listRepos`, `api.addRepo`, `api.setRepoEnabled`, `api.deleteRepo`, `ApiError`, `ConfirmButton`, `Switch`, `Input`, `Button`, `Alert`, `Skeleton`.
- Produces: the default exports `LimitsSection()` and `ReposSection({ connected })`. Nothing uses them until Task 3.

- [ ] **Step 1: The limits section**

`web/src/components/setup/LimitsSection.tsx`:

```tsx
import { useEffect, useState } from 'react'
import { api, ApiError } from '@/api.ts'
import type { Limits } from '@/api.ts'
import { diagnosisBar, limitsText } from '@/setup.ts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

/** What Remedy does on its own, and how much of it. The server's environment sets the limits. */
export default function LimitsSection() {
  const [limits, setLimits] = useState<Limits | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    api
      .getLimits()
      .then(setLimits)
      .catch((e: unknown) => setError(e instanceof ApiError ? e.message : 'Could not load the limits'))
  }, [])

  const bar = limits ? diagnosisBar(limits) : null

  return (
    <section className="flex flex-col gap-3.5">
      <h2 className="text-xs font-semibold text-muted-foreground">My limits</h2>
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {!limits && !error && <Skeleton className="h-24" />}
      {limits && (
        <div className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
          <p className="font-serif text-[17px] leading-relaxed text-pretty">{limitsText(limits)}</p>
          {bar && (
            <div className="flex flex-col gap-2">
              <span className="text-[13px] text-muted-foreground">
                Automatic diagnoses in the last 24 hours: {bar.used} of {bar.max}
              </span>
              <div
                role="progressbar"
                aria-label="Automatic diagnoses in the last 24 hours"
                aria-valuemin={0}
                aria-valuemax={bar.max}
                aria-valuenow={Math.min(bar.used, bar.max)}
                className="h-2 overflow-hidden rounded-full bg-input"
              >
                <div className={cn('h-full rounded-full', bar.full ? 'bg-destructive' : 'bg-primary')} style={{ width: `${Math.round(bar.ratio * 100)}%` }} />
              </div>
            </div>
          )}
          <span className="text-[13px] text-subtle">The server's environment sets these limits. They cannot be changed here.</span>
        </div>
      )}
    </section>
  )
}
```

- [ ] **Step 2: The repositories section**

`web/src/components/setup/ReposSection.tsx`:

```tsx
import { useCallback, useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { api, ApiError } from '@/api.ts'
import type { Repo } from '@/api.ts'
import { repoSubline, validRepoName } from '@/setup.ts'
import ConfirmButton from '@/components/ConfirmButton'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'

/** The repositories Remedy watches for failed checks. Without a GitHub connection there is nothing to list. */
export default function ReposSection({ connected }: { connected: boolean }) {
  const [repos, setRepos] = useState<Repo[] | null>(null)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [formError, setFormError] = useState('')

  const reload = useCallback(async () => {
    try {
      setRepos(await api.listRepos())
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not load the repositories')
    }
  }, [])

  useEffect(() => {
    if (connected) void reload()
  }, [connected, reload])

  async function act(action: () => Promise<unknown>) {
    setBusy(true)
    setError('')
    try {
      await action()
      await reload()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Request failed')
    } finally {
      setBusy(false)
    }
  }

  function add(e: FormEvent) {
    e.preventDefault()
    if (!validRepoName(name)) {
      setFormError('Use owner/name, for example jaydee94/homelab.')
      return
    }
    setFormError('')
    void act(async () => {
      await api.addRepo(name.trim())
      setName('')
    })
  }

  return (
    <section className="flex flex-col gap-3.5">
      <h2 className="text-xs font-semibold text-muted-foreground">Repositories I watch</h2>
      <div className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
        {!connected ? (
          <p className="text-muted-foreground">Connect GitHub first.</p>
        ) : (
          <>
            <form onSubmit={add} className="flex flex-col gap-2">
              <div className="flex gap-2">
                <Input
                  value={name}
                  onChange={(e) => {
                    setName(e.target.value)
                    setFormError('')
                  }}
                  placeholder="owner/name"
                  aria-label="Repository to add"
                  aria-invalid={formError !== ''}
                  autoComplete="off"
                  className="h-11 text-base md:text-sm"
                />
                <Button type="submit" disabled={busy || name.trim() === ''} className="h-11">
                  Add
                </Button>
              </div>
              {formError && (
                <span role="alert" className="text-[13px] text-destructive">
                  {formError}
                </span>
              )}
            </form>

            {repos === null ? null : repos.length === 0 ? (
              <p className="text-[13px] text-muted-foreground">No repositories yet. Add one above and I'll start watching it.</p>
            ) : (
              <ul className="flex flex-col divide-y divide-border">
                {repos.map((repo) => (
                  <li key={repo.id} className="flex items-center gap-3 py-3.5 first:pt-0 last:pb-0">
                    <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                      <span className="font-mono text-[13px] break-all">{repo.fullName}</span>
                      <span className="text-xs text-muted-foreground">{repoSubline(repo)}</span>
                      {repo.lastError && <span className="text-xs break-words text-destructive">{repo.lastError}</span>}
                    </div>
                    <Switch
                      checked={repo.enabled}
                      disabled={busy}
                      aria-label={`Watch ${repo.fullName}`}
                      onCheckedChange={(enabled) => void act(() => api.setRepoEnabled(repo.id, enabled))}
                    />
                    <ConfirmButton label="Remove" confirmLabel="Confirm remove" disabled={busy} onConfirm={() => void act(() => api.deleteRepo(repo.id))} />
                  </li>
                ))}
              </ul>
            )}
          </>
        )}

        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
      </div>
    </section>
  )
}
```

- [ ] **Step 3: Verify and commit**

Run: `npm test --prefix web`, `npm run lint --prefix web`, `npm run build --prefix web`. Expected: all pass, lint prints nothing.

```sh
git add web/src/components/setup
git commit -m "feat(web): the limits and repositories sections of Setup" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The GitHub section, the page, the route and the sign-out

**Files:**
- Create: `web/src/components/setup/GitHubSection.tsx`, `web/src/SetupPage.tsx`
- Modify: `web/src/shellContext.ts`, `web/src/components/AppLayout.tsx`, `web/src/App.tsx`, `README.md`, `docs/runbook/first-real-run.md`
- Delete: `web/src/SettingsPage.tsx`, `web/src/GitHubConnectionCard.tsx`, `web/src/ReposCard.tsx`, `web/src/LimitsCard.tsx`, `web/src/components/LegacyPage.tsx`

**Interfaces:**
- Consumes: Tasks 1 and 2; `api.getConnection`, `api.putConnection`, `api.checkConnection`, `api.deleteConnection`, `GitHubConnection`, `ConfirmButton`, `timeAgo`, `connectionView`, `useShellHeader` is not needed (the default header of `/settings` is "Setup").
- Produces: `GitHubSection({ connection, onChange })`, the default export `SetupPage()`, `useSignOut()`, the routes `settings` and the catch-all outside `LegacyPage`.

- [ ] **Step 1: The sign-out in the shell context**

In `web/src/shellContext.ts` extend `ShellApi` and the default:

```ts
export interface ShellApi {
  setHeader: (header: Header | null) => void
  /** Signs the maintainer out (the sidebar has a button; a phone has it at the end of Setup). */
  signOut: () => void
}

export const ShellContext = createContext<ShellApi>({ setHeader: () => {}, signOut: () => {} })
```

and add next to `useShellHeader`:

```ts
/** The way to sign out, for a page that offers it. */
export function useSignOut(): () => void {
  return useContext(ShellContext).signOut
}
```

In `web/src/components/AppLayout.tsx` change the memo to `useMemo(() => ({ setHeader: setCustom, signOut: onSignOut }), [onSignOut])`.

- [ ] **Step 2: The GitHub section**

`web/src/components/setup/GitHubSection.tsx`:

```tsx
import { useState } from 'react'
import type { FormEvent } from 'react'
import { api, ApiError } from '@/api.ts'
import type { GitHubConnection } from '@/api.ts'
import { timeAgo } from '@/incidents.ts'
import { connectionView } from '@/setup.ts'
import ConfirmButton from '@/components/ConfirmButton'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

interface Props {
  connection: GitHubConnection
  onChange: (c: GitHubConnection) => void
}

/** How Remedy sees GitHub. The token is write-only: only its hint is ever shown, and the field is cleared after each request. */
export default function GitHubSection({ connection, onChange }: Props) {
  const [token, setToken] = useState('')
  const [editing, setEditing] = useState(false)
  const [busy, setBusy] = useState(false)
  const [checking, setChecking] = useState(false)
  const [error, setError] = useState('')

  const showForm = !connection.connected || editing
  const view = connection.connected && connection.status ? connectionView(connection.status) : null

  async function run(action: () => Promise<GitHubConnection>) {
    setBusy(true)
    setError('')
    try {
      onChange(await action())
      setToken('')
      setEditing(false)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Request failed')
    } finally {
      setBusy(false)
    }
  }

  function save(e: FormEvent) {
    e.preventDefault()
    if (busy || token.trim() === '') return
    void run(() => api.putConnection(token))
  }

  async function check() {
    setChecking(true)
    try {
      await run(api.checkConnection)
    } finally {
      setChecking(false)
    }
  }

  async function disconnect() {
    setBusy(true)
    setError('')
    try {
      await api.deleteConnection()
      onChange({ connected: false })
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Request failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
      {connection.connected ? (
        <>
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            {view && (
              <span className={cn('flex items-center gap-2 rounded-2xl px-3 py-1.5 text-[13px] font-semibold', view.soft, view.text)}>
                <span aria-hidden className={cn('size-2 rounded-full', view.dot)} />
                {view.label}
              </span>
            )}
            {connection.checkedAt && <span className="text-[13px] text-muted-foreground">checked {timeAgo(connection.checkedAt)}</span>}
          </div>
          <p className="font-serif text-[17px] leading-relaxed text-pretty break-words">
            I read pull requests and check runs as <strong className="font-semibold">@{connection.login}</strong> with token …{connection.tokenHint}. It's
            stored encrypted and never shown again.
          </p>
          {connection.statusDetail && (
            <Alert variant="destructive">
              <AlertDescription>{connection.statusDetail}</AlertDescription>
            </Alert>
          )}
        </>
      ) : (
        <p className="font-serif text-[17px] leading-relaxed text-pretty">I can't see GitHub yet. Paste a fine-grained token and I'll start watching.</p>
      )}

      {showForm && (
        <form onSubmit={save} className="flex flex-col gap-3">
          <Input
            type="password"
            autoComplete="off"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            placeholder="github_pat_…"
            aria-label="GitHub token"
            className="h-11 text-base md:text-sm"
          />
          <p className="text-[13px] text-muted-foreground">
            Use a fine-grained personal access token with read-only access to the repositories: Metadata, Contents, Pull requests, Actions and Checks.
          </p>
          <div className="flex gap-2">
            <Button type="submit" disabled={busy || token.trim() === ''} className="h-11">
              {connection.connected ? 'Replace token' : 'Connect'}
            </Button>
            {editing && (
              <Button
                type="button"
                variant="ghost"
                className="h-11"
                onClick={() => {
                  setEditing(false)
                  setToken('')
                  setError('')
                }}
              >
                Cancel
              </Button>
            )}
          </div>
        </form>
      )}

      {connection.connected && !editing && (
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" disabled={busy} onClick={() => void check()}>
            {checking ? 'Checking…' : 'Check connection'}
          </Button>
          <Button variant="outline" size="sm" disabled={busy} onClick={() => setEditing(true)}>
            Replace token
          </Button>
          <ConfirmButton label="Disconnect" confirmLabel="Confirm: also removes the repositories" disabled={busy} onConfirm={() => void disconnect()} />
        </div>
      )}

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
    </div>
  )
}
```

- [ ] **Step 3: The page**

`web/src/SetupPage.tsx`:

```tsx
import { useEffect, useState } from 'react'
import { api, ApiError } from './api.ts'
import type { GitHubConnection } from './api.ts'
import { useSignOut } from './shellContext.ts'
import GitHubSection from '@/components/setup/GitHubSection'
import LimitsSection from '@/components/setup/LimitsSection'
import ReposSection from '@/components/setup/ReposSection'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'

/** What Remedy watches and how much it does on its own. One column; on a phone "Sign out" is the last thing on the page. */
export default function SetupPage() {
  const signOut = useSignOut()
  const [connection, setConnection] = useState<GitHubConnection | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    api
      .getConnection()
      .then(setConnection)
      .catch((e: unknown) => setError(e instanceof ApiError ? e.message : 'Could not load the setup'))
  }, [])

  return (
    <div className="mx-auto flex max-w-190 flex-col gap-8 px-4 py-5 md:px-10 md:py-12">
      <div className="flex flex-col gap-1">
        <h1 className="font-serif text-[clamp(26px,3vw,34px)] font-normal">Setup</h1>
        <span className="text-muted-foreground">What I watch, and how much I do on my own.</span>
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {connection ? (
        <>
          <section className="flex flex-col gap-3.5">
            <h2 className="text-xs font-semibold text-muted-foreground">GitHub</h2>
            <GitHubSection connection={connection} onChange={setConnection} />
          </section>
          <ReposSection connected={connection.connected} />
          <LimitsSection />
        </>
      ) : (
        !error && <Skeleton className="h-48" />
      )}

      <div className="border-t border-border pt-4 md:hidden">
        <button
          type="button"
          onClick={signOut}
          className="h-11 rounded-full px-1 text-[15px] text-muted-foreground outline-none transition-colors hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          Sign out
        </button>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: The routes, the deletions and the docs**

In `web/src/App.tsx`: import `SetupPage from './SetupPage.tsx'`; remove the imports of `LegacyPage` and `SettingsPage`; add a small component next to `IncidentRoute`:

```tsx
/** A page that does not exist, in the same column as the others. */
function NotFoundPage() {
  return (
    <div className="mx-auto max-w-190 px-4 py-10 md:px-10">
      <NotFound />
    </div>
  )
}
```

use it in `IncidentRoute` for an invalid id (replacing the inline wrapper), and make the routes:

```tsx
        <Route path="settings" element={<SetupPage />} />
        <Route path="*" element={<NotFoundPage />} />
```

with no `LegacyPage` group left. `git rm web/src/SettingsPage.tsx web/src/GitHubConnectionCard.tsx web/src/ReposCard.tsx web/src/LimitsCard.tsx web/src/components/LegacyPage.tsx`. `grep -rn "SettingsPage\|GitHubConnectionCard\|ReposCard\|LimitsCard\|LegacyPage" web/src` must find nothing (a comment that names `LegacyPage` in `AppLayout` or elsewhere is removed or reworded).

Docs: in `README.md` and `docs/runbook/first-real-run.md` the page is now called **Setup** (find "**Settings**" with grep; read the context before editing; change only the name of this page, not the GitHub URLs `github.com/settings/...` and not the generic word "settings" in sentences about configuration). Report what you changed.

- [ ] **Step 5: Verify and commit**

Run: `npm test --prefix web`, `npm run lint --prefix web`, `npm run build --prefix web`. Expected: all pass, lint prints nothing.

```sh
git add web/src README.md docs/runbook
git commit -m "feat(web): Setup" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Verification (run by the controller, in a real browser)

No code changes unless a defect is found (a defect goes to a fix dispatch). Nothing here is committed.

- [ ] **Step 1: Full checks.** `make check` and `go test -tags webui ./web/` after `make web-build`. Expected: PASS.

- [ ] **Step 2: Run the app** with a throwaway database in the scratchpad (the scripts of part 4: `t4-up.sh`-style: build the UI, build the server with `-tags webui`, start it with the admin password, runner token and a random master key, seed with sqlite). Seed a GitHub connection (`github_connections`, ciphertext any bytes: the API only shows the hint and the status; check how `status` and `statusDetail` are stored, also seed variants: `ok`, `error` with a long `status_detail`, `undecryptable`), repositories (a long name, one enabled, one disabled, one with a long `last_error`, one never polled), and `GET /api/limits` from the server's environment (start it once with defaults, once with `REMEDY_DIAGNOSE_MAX_PER_DAY=0`, once with a small limit such as 2 and seed activity so that `diagnosesLast24h` is above the limit: see how it is counted in `internal/store`). A real token check needs GitHub: the connection check will fail against the fake token (that is a good error to look at); do not use a real token.

- [ ] **Step 3: Check Setup** at 1280x900 and 390x844: connected (each status), not connected ("I can't see GitHub yet." and the Connect button disabled when the field is empty), Check connection ("Checking…" while it runs, then the error card), Replace token (field, Cancel clears it), Disconnect (two steps, the repositories section says "Connect GitHub first."), adding a repository (wrong forms refused in the browser with the sentence, a right form goes to the API and its refusal shows in place), the switch (paused sub line), Remove (two steps), the three limits texts and the bar (at 0, partial, full), the loading and error states (stop the server for one request), the token field never shows a value after a request, one `h1`, no horizontal overflow with a long name and a long error, focus rings, and on a phone "Sign out" is the last element and signs out (on a desktop it is not repeated on the page; the sidebar has it).

- [ ] **Step 4: Check the rest of the app** still works without `LegacyPage`: an unknown path (`/nope`) shows "Page not found" in the column; every other route is unchanged.

- [ ] **Step 5: Clean up.** Stop the processes you started (by PID), delete the throwaway database, `.playwright-mcp/` and any `*.png` (also in the main checkout).

---

## Self-Review

**Spec coverage (spec 8):** GitHub: the status chip (Connected, Error, Cannot decrypt) with "checked N min ago", the sentence with `@login` and the token hint, `statusDetail` as an error card, "Check connection" with "Checking…", "Replace token", "Disconnect" with `ConfirmButton`, not connected: "I can't see GitHub yet. Paste a fine-grained token and I'll start watching." with the field, Connect and the hint on the read-only permissions, the token write-only (Tasks 1, 3). Repositories: the name, "<default branch>, polled N min ago" or "never polled", `lastError` in red, a `Switch` with "paused", "Remove" with `ConfirmButton`, add by `owner/name` with the browser's check and its fixed sentence, "Connect GitHub first." (Tasks 1, 2). Limits: the Literata paragraph from `GET /api/limits` (`limitsText`), the off sentence, the bar with `diagnosesLast24h` of N, the note on the environment (Tasks 1, 2). On a phone "Sign out" is the last element (Task 3). **Deviations, recorded:** the bar is labelled "in the last 24 hours" (the spec says "today"; the number is a rolling 24 hours); "Remove" uses the existing `ConfirmButton` style (outline), not a special quiet variant; `LegacyPage` is deleted because Setup is its last page (the catch-all gets its own container). **Placeholder scan:** none.

**Type consistency:** `limitsText`, `diagnosisBar`, `validRepoName`, `repoSubline`, `connectionView` (Task 1) are used by the sections (Tasks 2, 3). `GitHubSection({ connection, onChange })` takes what `SetupPage` passes. `useSignOut()` reads the new `signOut` of the shell context that `AppLayout` provides.

**Review Focus coverage:** 1 (Task 3 code; Task 4), 2 (Task 3 code; Task 4), 3 (Task 2 code; Task 4), 4 (Task 1 tests; Task 2), 5 (Task 1 tests; Task 2), 6 (Task 3 code; Task 4), 7 (Tasks 2 to 4), 8 (Task 2 code).
