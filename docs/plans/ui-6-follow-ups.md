# UI redesign, follow-ups Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Work off the open follow-ups that the pull requests of parts 3 to 5 of the conversation UI (#116 to #118) listed as "known, left for later": the stale preview after a failed diagnosis, a 401 that does not sign out, low-contrast tokens, the clock that freezes in render, dates on thread times, small touch targets, the token mirrored into a DOM attribute, focus lost on disabled controls, and a handful of smaller accessibility and layout items.

**Architecture:** Pure helpers with tests (`incidentPreview`, `dayTimeLabel`, `limitBytes`, a token contrast test), one place for the 401 handling (`api.ts` and `App`), one shared clock (`useClock`) for coarse "N min ago" text, and small component changes. No backend change.

**Tech Stack:** React 19, React Router 8, TypeScript 7, Tailwind 4, `node --test`.

**Sources:** the "Notes for review" and "Known, left for later" lists of PRs #116, #117 and #118, and the memory note `project_ui-conversation-redesign.md`. Spec: [`docs/specs/2026-10-05-ui-conversation-redesign-design.md`](../specs/2026-10-05-ui-conversation-redesign-design.md) (3.2 tokens, 9 trust boundaries).

**Not in this plan (they need a backend change or a decision, or have no evidence of a problem):** the digest that can count a failed re-diagnosis for up to 24 hours (needs a "diagnosed at" field), `GET /api/runs?incident=` carrying full responder prompts and the cap of 50, `runSteps` recomputed on every event, announcing new thread messages to screen readers, the Undo button being last in the tab order, focus returning after "Diagnose now" and after an answered ask in the thread.

## Global Constraints

- Everything in the repo is English: code, comments, UI copy, docs, commit messages.
- `web/tsconfig.app.json` sets `erasableSyntaxOnly` and `verbatimModuleSyntax`: no enums or constructor parameter properties, `import type` for types.
- Pure logic modules import only with relative paths and `.ts` extensions and contain only erasable TypeScript. Components may use the `@/` alias.
- **Untrusted text** stays React text; a sentence's structure never comes from it. **The GitHub token is write-only:** never shown, stored or logged.
- Palette and tokens: no hard-coded palette colours in new code; a colour change is made in `web/src/index.css` only.
- `npm run lint` must print no warnings (a `setState` inside an effect body and `Date.now()` in render are warnings in this project).
- Commits end with the line `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>` (exactly this model name).
- In a worktree-isolated session the Bash tool refuses commands that name git in a compound or variable form: use plain separate commands or a script file with literal paths (see `CLAUDE.md`); run npm with `--prefix web`. Use `command ls`.
- Work on a branch `feat/ui-6-follow-ups`, never on `main`.

## Review Focus

1. A 401 from any request while signed in ends the session in the UI exactly once and shows a notice on the login page; a wrong password (401 from `/api/login`) and the first `GET /api/me` (401 when nobody is signed in) must NOT count as an expired session; polling stops because the pages unmount. (Task 2.)
2. Signing out when the request fails keeps the user signed in and says so in a toast; it never leaves an unhandled rejection. (Task 2.)
3. The new colours keep the hierarchy (subtle stays quieter than muted) and meet WCAG AA: small text at least 4.5:1 against the background, the card and the sidebar; the border of a field at least 3:1 against the background and the card. A test reads the tokens from `index.css` and pins that. (Task 3.)
4. A time shown as "N min ago" moves on while the page stays open, with one timer for the whole app, not one per component; nothing reads the clock in render. (Task 4.)
5. A thread or a run over several days: a message from another day shows the date with its time; a pill has the full time in its `title`. (Task 4.)
6. The token field never has a `value` attribute (an uncontrolled input), is emptied on success and on Cancel, and Connect/Replace stays disabled for an empty or blank field. (Task 5.)
7. A switch or a button of Setup that is busy keeps the keyboard focus (`aria-disabled`, not `disabled`) and ignores clicks while busy. (Task 5.)
8. The reason of an approval is limited to 500 **bytes** (the server's limit), not 500 characters: a field full of `ä` or emoji cannot exceed it and never ends in a split character. (Task 1 tests, Task 5.)
9. 390 px: nothing scrolls sideways; the "About this incident" panel is under the thread at full width; `sm` buttons are at least 40 px high on a phone. (Tasks 3, 6.)

---

## File Structure

| File | Change | Responsibility |
|---|---|---|
| `web/src/conversation.ts` + `conversation.test.ts` | modify | `incidentPreview`: a failed diagnosis |
| `web/src/timeline.ts` + test | modify | `dayTimeLabel` |
| `web/src/approvals.ts` + test | modify | `limitBytes` |
| `web/src/api.ts`, `web/src/App.tsx`, `web/src/Login.tsx` | modify | the 401 handler, the notice |
| `web/src/shellContext.ts`, `web/src/components/AppLayout.tsx`, `web/src/components/shell/Sidebar.tsx` | modify | `useSignOut` with a toast on failure |
| `web/src/index.css`, `web/src/incidents.ts`, `web/src/components/ui/{button,input}.tsx`, `web/src/components/conversation/Composer.tsx`, `web/src/AskPage.tsx`, `web/src/IncidentThreadPage.tsx` | modify | tokens, borders, touch size, the panel |
| `web/src/tokens.test.ts` | create | contrast of the tokens |
| `web/src/useClock.ts` | create | the shared clock |
| the components that print "N min ago" | modify | use it |
| `web/src/components/conversation/{EventPill,ApprovalAsk,DiagnosisMessage,StepCard}.tsx`, `web/src/components/setup/{GitHubSection,ReposSection}.tsx`, `web/src/ToolCallsCard.tsx`, `web/src/RunPage.tsx` | modify | the component items of Task 5 |

---

### Task 1: The logic (preview, day-time label, byte limit)

**Files:**
- Modify: `web/src/conversation.ts`, `web/src/conversation.test.ts`, `web/src/timeline.ts`, `web/src/timeline.test.ts` (create it if it does not exist), `web/src/approvals.ts`, `web/src/approvals.test.ts` (create it if it does not exist)

**Interfaces:**
- Produces: `incidentPreview` (new case), `dayTimeLabel(iso: string, now?: number): string`, `limitBytes(text: string, max: number): string`.

- [ ] **Step 1: Tests first**

1. `conversation.test.ts`: an `open` incident without a diagnosis whose `lastDiagnosisAt` is set reads `My last diagnosis didn't finish.` whether `autoDiagnose` is true or false; the same incident without `lastDiagnosisAt` still reads "I haven't looked yet." / the manual sentence; an `open` incident WITH a stored diagnosis still shows the summary; a waiting ask, `diagnosing`, `ignored` and `resolved` keep their precedence (the new case comes after the diagnosis check and before the `autoDiagnose` check).
2. `timeline.test.ts`: `dayTimeLabel(iso, now)`: an ISO time on the same local day as `now` equals `timeOfDay(iso)`; another day reads `<day and short month>, <time>` (the test builds the dates with `new Date(2026, 9, 5, 12, 0)` style local constructors so it does not depend on the time zone: assert `!== timeOfDay(iso)`, `includes(timeOfDay(iso))` and `includes(',')`); a time in the future of another day is also dated.
3. `approvals.test.ts`: `limitBytes('abc', 500)` is unchanged; 300 times `ä` (2 bytes each) is cut to 250 characters (500 bytes) and never ends in a half character; 200 emoji (4 bytes) are cut to 125; ASCII is cut at exactly `max`; an empty string and `max` 0 work; the result always satisfies `new TextEncoder().encode(result).length <= max`.

- [ ] **Step 2: Run them to see them fail** (`npm test --prefix web`).

- [ ] **Step 3: Implement**

`conversation.ts`: in `incidentPreview` after `if (incident.diagnosis) return incident.diagnosis.summary`:

```ts
  if (incident.state === 'open' && incident.lastDiagnosisAt !== undefined) return "My last diagnosis didn't finish."
```

(`lastDiagnosisAt` is set when any diagnosis starts, automatic or by hand, and the incident is `open` without a diagnosis only when that diagnosis failed: see `internal/store/diagnosis.go`.)

`timeline.ts`:

```ts
/** A time for a message: just the time of day when it is from today, else the day and the month first. */
export function dayTimeLabel(iso: string, now: number = Date.now()): string {
  const at = new Date(iso)
  const today = new Date(now)
  const sameDay = at.getFullYear() === today.getFullYear() && at.getMonth() === today.getMonth() && at.getDate() === today.getDate()
  return sameDay ? timeOfDay(iso) : `${at.toLocaleDateString([], { day: 'numeric', month: 'short' })}, ${timeOfDay(iso)}`
}
```

`approvals.ts`:

```ts
/** `text` cut to at most `max` UTF-8 bytes, on a character boundary (the server limits some fields in bytes, not characters). */
export function limitBytes(text: string, max: number): string {
  const encoder = new TextEncoder()
  if (encoder.encode(text).length <= max) return text
  let out = ''
  let bytes = 0
  for (const ch of text) {
    const n = encoder.encode(ch).length
    if (bytes + n > max) break
    out += ch
    bytes += n
  }
  return out
}
```

- [ ] **Step 4: Run the tests, lint and build.** Expected: all pass, lint prints nothing.

- [ ] **Step 5: Commit**

```sh
git add web/src
git commit -m "feat(web): the preview of a failed diagnosis, day and time labels, a byte limit" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: A 401 ends the session; signing out can fail

**Files:**
- Modify: `web/src/api.ts`, `web/src/App.tsx`, `web/src/Login.tsx`, `web/src/shellContext.ts`, `web/src/components/AppLayout.tsx`, `web/src/components/shell/Sidebar.tsx`, `web/src/SetupPage.tsx`

**Interfaces:**
- Produces: `setUnauthorizedHandler(fn | null)` in `api.ts`; `Login({ onLoggedIn, notice? })`; `useSignOut()` that never rejects and toasts on failure; `ShellApi.signOut: () => void | Promise<void>`.

- [ ] **Step 1: The handler in `api.ts`**

```ts
let onUnauthorized: (() => void) | null = null

/** Called when a request answers 401 while the maintainer is signed in. The login and the first `/api/me` do not count. */
export function setUnauthorizedHandler(handler: (() => void) | null) {
  onUnauthorized = handler
}
```

In `request`, in the `!res.ok` branch, before throwing: `if (res.status === 401 && path !== '/api/login' && path !== '/api/me') onUnauthorized?.()`. The error is still thrown (callers keep their own handling).

- [ ] **Step 2: `App`**

Keep `auth` as it is and add `const [expired, setExpired] = useState(false)`. While `auth === 'in'`, an effect registers `setUnauthorizedHandler(() => { setExpired(true); setAuth('out') })` and removes it in its cleanup (`setUnauthorizedHandler(null)`). Many polls may answer 401 at once: the handler is idempotent. `Login` gets `notice={expired ? 'Your session ended. Please sign in again.' : undefined}` and `onLoggedIn={() => { setExpired(false); setAuth('in') }}`. A deliberate sign out does not set `expired`. `signOut` in `App` stays `await api.logout(); setAuth('out')` and may reject (the callers handle it).

- [ ] **Step 3: `Login`**

A `notice` prop: a muted line (`role="status"`, `text-muted-foreground`, same column as the hint) above the form. It is not an error and not red.

- [ ] **Step 4: `useSignOut`**

`ShellApi.signOut` becomes `() => void | Promise<void>` (default `() => {}`), `AppLayout`'s `onSignOut` prop likewise. `useSignOut()` in `shellContext.ts`:

```ts
/** The way to sign out, for a page or the sidebar. A failed request keeps the session and says so; it never rejects. */
export function useSignOut(): () => void {
  const { signOut } = useContext(ShellContext)
  const toast = useToast()
  return useCallback(() => {
    void Promise.resolve(signOut()).catch(() => toast.show('Could not sign out. Check the connection and try again.'))
  }, [signOut, toast])
}
```

(import `useCallback` and `useToast` from `./toast.ts`). `Sidebar` uses `useSignOut()` and loses its `onSignOut` prop (`AppLayout` stops passing it); `SetupPage` already calls `useSignOut()`.

- [ ] **Step 5: Verify and commit.** `npm test --prefix web`, `npm run lint --prefix web`, `npm run build --prefix web`. No component test runner: Task 6 checks it in a browser (delete the session on the server by restarting it while a page is open, a wrong password, an offline sign out).

```sh
git add web/src
git commit -m "feat(web): a 401 ends the session, and signing out can fail" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Tokens, field borders, touch size and the panel

**Files:**
- Modify: `web/src/index.css`, `web/src/incidents.ts`, `web/src/components/ui/button.tsx`, `web/src/components/ui/input.tsx`, `web/src/components/conversation/Composer.tsx`, `web/src/AskPage.tsx`, `web/src/IncidentThreadPage.tsx`
- Create: `web/src/tokens.test.ts`

- [ ] **Step 1: The contrast test first** (`web/src/tokens.test.ts`)

Read `web/src/index.css` with `node:fs` (path relative to the test file: `new URL('./index.css', import.meta.url)`), parse the custom properties of the first `:root` block (`--name: #rrggbb;`), compute the WCAG relative luminance and ratio (write the formula inline), and assert:

- `--subtle` against `--background`, `--card` and `--sidebar`: at least 4.5.
- `--muted-foreground` against the same three: at least 4.5, and `--subtle` is quieter than `--muted-foreground` (its luminance is lower).
- `--field` against `--background` and `--card`: at least 3.
- `--neutral` is not used as text on `--soft-ignored` any more: no assertion, but assert `incidentStateText.ignored` (imported from `./incidents.ts`) is not `text-neutral`.

Run it: it fails (no `--field`, `--subtle` too dark).

- [ ] **Step 2: The tokens**

In `index.css`: `--subtle: #93877a;` (about 4.7:1 on the card, 5.1:1 on the background, 5.3:1 on the sidebar) and a new `--field: #7a6f62;` (about 3.7:1 on the background, 3.4:1 on the card) with `--color-field: var(--field);` next to the other `--color-*` entries. `incidentStateText.ignored` in `incidents.ts` becomes `text-muted-foreground` (the ignored chip's text was `text-neutral` on `bg-soft-ignored`, 3.8:1).

- [ ] **Step 3: Use the field border where a field is the only thing that says "type here"**

`components/ui/input.tsx`: the border class `border-input` becomes `border-field` (keep every other class). `Composer.tsx`'s form and `AskPage.tsx`'s form: `border-input` becomes `border-field` (their `focus-within:border-ring` stays). Chips, buttons, cards and empty states keep `border-input`.

- [ ] **Step 4: Touch size**

`components/ui/button.tsx`: the `sm` size gets `max-md:h-10` (40 px on a phone, 32 px on a desktop) and the `xs` size `max-md:h-8`; nothing else changes. Check that the Setup buttons that already set `h-10` and the `ConfirmButton` (size `sm`) still look right.

- [ ] **Step 5: The panel of the thread under 520 px**

In `IncidentThreadPage.tsx` the `aside` ("About this incident") is `flex-[0_1_280px]` in a wrapping row, so under about 520 px it wraps to 280 px wide and left-aligned. On a phone it must be full width under the thread: add `max-md:flex-[1_1_100%]` (and keep `m-4`), so it spans the row. Check at 390 px and at 700 px (the panel then sits under the thread at full width or beside it, never at an odd 280 px).

- [ ] **Step 6: Verify and commit.** `npm test --prefix web` (the new token test passes), `npm run lint --prefix web`, `npm run build --prefix web`.

```sh
git add web/src
git commit -m "feat(web): readable tokens, visible field borders, 40 px buttons on a phone" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: One clock, dates on thread times

**Files:**
- Create: `web/src/useClock.ts`
- Modify: `web/src/IncidentThreadPage.tsx`, `web/src/RunPage.tsx`, `web/src/NeedsYouPage.tsx`, `web/src/components/ask/RecentRunRow.tsx`, `web/src/components/shell/Sidebar.tsx`, `web/src/components/needs/AnsweredRow.tsx`, `web/src/components/conversations/IncidentCard.tsx`, `web/src/ToolCallsCard.tsx`, `web/src/components/setup/GitHubSection.tsx`, `web/src/components/setup/ReposSection.tsx`, `web/src/components/conversation/EventPill.tsx`

- [ ] **Step 1: The shared clock** (`web/src/useClock.ts`)

One module-level timer for the whole app, started with the first subscriber and stopped with the last, ticking every 30 s, read through `useSyncExternalStore`:

```ts
import { useSyncExternalStore } from 'react'

const TICK_MS = 30_000
const listeners = new Set<() => void>()
let now = Date.now()
let timer: ReturnType<typeof setInterval> | undefined

function subscribe(listener: () => void) {
  listeners.add(listener)
  if (listeners.size === 1) {
    now = Date.now()
    timer = setInterval(() => {
      now = Date.now()
      listeners.forEach((l) => l())
    }, TICK_MS)
  }
  return () => {
    listeners.delete(listener)
    if (listeners.size === 0) clearInterval(timer)
  }
}

/** The current time in milliseconds, refreshed every 30 seconds for every component that asks: for "N min ago", never for logic. */
export function useClock(): number {
  return useSyncExternalStore(subscribe, () => now, () => now)
}
```

- [ ] **Step 2: Use it where `timeAgo` or `repoSubline` reads the clock**

Every component that calls `timeAgo(x)` (or `repoSubline(repo)`) without a `now` takes `const now = useClock()` once and passes it: `timeAgo(x, now)`. Find them with `grep -rn "timeAgo(" web/src` (the components listed above; `GitHubSection` and `ReposSection` use `useNow(60_000)` today: switch them to `useClock()` too, so there is one clock). A list component that renders rows through a child (for example `IncidentCard` or `AnsweredRow`) calls `useClock()` in the row component. The pure modules keep their `now` parameters and defaults (tests and `digestText` use them).

- [ ] **Step 3: Dates on messages and a time on pills**

In `IncidentThreadPage.tsx` and `RunPage.tsx` the small line of a Remedy message ("Remedy · 11:10 PM · ...", "Remedy · asked 4 min ago" stays) uses `dayTimeLabel(at, now)` instead of `timeOfDay(at)` (`now` from `useClock()`); `runMeta(run, time)` keeps taking a string, pass the label. `EventPill` gets an optional `title?: string` prop that goes on the pill's outer element; the thread passes `new Date(item.at).toLocaleString()`. Nothing else in the pills changes.

- [ ] **Step 4: Verify and commit.** `npm test --prefix web`, `npm run lint --prefix web` (no `Date.now()` in render anywhere you touched), `npm run build --prefix web`. `grep -rn "timeAgo(" web/src` shows only calls with a `now` (and the definition, the pure modules and tests).

```sh
git add web/src
git commit -m "feat(web): one clock for every N min ago, dates on thread times" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: The components

**Files:**
- Modify: `web/src/components/setup/GitHubSection.tsx`, `web/src/components/setup/ReposSection.tsx`, `web/src/components/conversation/ApprovalAsk.tsx`, `web/src/components/conversation/DiagnosisMessage.tsx`, `web/src/components/conversation/StepCard.tsx`, `web/src/ToolCallsCard.tsx`, `web/src/RunPage.tsx`

- [ ] **Step 1: The token as an uncontrolled field**

`GitHubSection`: the token input has no `value`/`onChange`; keep a `ref` to it and a boolean `hasText` (set from `onInput`: `ref.current.value.trim() !== ''`) that drives the disabled state of Connect/Replace token. `save` reads `ref.current.value.trim()`, sends it, and on success clears the field (`ref.current.value = ''`, `hasText` false); Cancel clears it too; a failed request keeps it (typo retry). With no `value` attribute React never mirrors the token into the DOM. Everything else (focus handling, `autoComplete`, the ignore attributes, `type="password"`) stays.

- [ ] **Step 2: `aria-disabled` while busy in Setup**

A control that disables itself while its own request runs loses the keyboard focus in Chrome. In `GitHubSection` ("Check connection", "Replace token", Connect/Replace submit) and `ReposSection` (the switch, "Add") use `aria-disabled={busy}` and ignore the click/change while busy (an early return in the handler), plus a style hook for the look (`aria-disabled:opacity-50 aria-disabled:pointer-events-none` does not stop keyboard activation: the handler guard does). The Radix `Switch` takes `aria-disabled` as a prop; guard `onCheckedChange` with `if (busy) return`. `ConfirmButton` and the Remove buttons keep `disabled`. Disabled-by-emptiness (Add with an empty field, Connect with an empty token) stays `disabled`: those controls are not focused by an action.

- [ ] **Step 3: The reason of an approval in bytes**

`ApprovalAsk`: the reason field's `onChange` stores `limitBytes(e.target.value, 500)` (import from `@/approvals.ts`); keep `maxLength={500}`.

- [ ] **Step 4: Headings and a list in the diagnosis**

`DiagnosisMessage`: the `Section` title is an `h2` (same classes) instead of a `span`; the list of files is a `ul` with an `li` per file (the same chip look), so a screen reader gets structure.

- [ ] **Step 5: The raw line of a step can be opened**

`StepCard`: when `step.raw` is longer than 160 characters, show a small button "Show all" / "Show less" (`aria-expanded`, a focus ring, `text-xs text-muted-foreground hover:text-foreground`) under the raw line; collapsed it keeps `line-clamp-3`, expanded it shows the whole line (still `break-all`, still text). Short lines have no button. Keep the card's look.

- [ ] **Step 6: One "Tool calls" title on the run page**

`ToolCallsCard` is used only by `RunPage`, inside a `details` whose summary already says "Tool calls (N)". Remove the card's own `CardHeader`/`CardTitle` "Tool calls" (keep the card and the list); remove imports that become unused.

- [ ] **Step 7: Verify and commit.** `npm test --prefix web`, `npm run lint --prefix web`, `npm run build --prefix web`.

```sh
git add web/src
git commit -m "feat(web): an uncontrolled token field, steady focus in Setup, structure in the diagnosis" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Verification (run by the controller, in a real browser)

No code changes unless a defect is found (a defect goes to a fix dispatch). Nothing here is committed.

- [ ] **Step 1: Full checks.** `make check` and `go test -tags webui ./web/` after `make web-build`. Expected: PASS.
- [ ] **Step 2: Run the app** with a throwaway database in the scratchpad (the scripts of part 5: `t5-up.sh` and `t5-start.sh`, `seed-ui3.sql`; add an `open` GitHub incident whose `last_diagnosis_at` is set and `diagnosis` is NULL, an activity row and a run from two days ago for the dates).
- [ ] **Step 3: Check.** The sidebar and the card of the failed-diagnosis incident say "My last diagnosis didn't finish."; restart the server while a page is open: the next poll shows the login with "Your session ended. Please sign in again.", a wrong password shows the red message and no notice, a correct one signs in; a sign out that fails (route the request to a failure) toasts and keeps the session; the colours at 1280 and 390 px (subtle text readable, the field borders visible on the composer, the ask field and the Setup fields); "N min ago" advances without a reload (wait about 35 seconds); a message from another day shows its date, a pill has a `title`; `sm` buttons are 40 px on a phone and 32 px on a desktop; the token field has no `value` attribute while typed (inspect the DOM), keeps focus through "Check connection" and the switch with the keyboard; the reason field stops at 500 bytes with `ä` (type or paste 300 of them); the diagnosis has `h2` headings and a file list; a long step line opens and closes; the run page has one "Tool calls" title; the panel is under the thread at full width at 390 and 700 px.
- [ ] **Step 4: Clean up.** Stop the processes you started, delete the throwaway database, `.playwright-mcp/` and any `*.png` (also in the main checkout).

---

## Self-Review

**Coverage of the follow-ups:** preview after a failed diagnosis (Task 1), 401 and polling after a 401 (Task 2), `signOut` error handling (Task 2), `text-subtle`, input border and the ignored chip contrast (Task 3), `sm` buttons (Task 3), the aside width (Task 3), `timeAgo` in render (Task 4), dates on thread times (Task 4), the token in the `value` attribute (Task 5), blur on disabled-while-busy (Task 5), 500 bytes versus characters (Tasks 1, 5), section titles as headings, the raw line of a step, the duplicate "Tool calls" title (Task 5). **Left out, with the reason, at the top.**

**Placeholder scan:** none; Tasks 3 to 5 describe changes to existing components in words where the code depends on what the file looks like now (the implementer reads it).

**Type consistency:** `incidentPreview`, `dayTimeLabel`, `limitBytes` (Task 1) are used by Tasks 4 and 5. `useSignOut` (Task 2) is used by `Sidebar` and `SetupPage`. `useClock` (Task 4) replaces the default clock of `timeAgo`/`repoSubline`.

**Review Focus coverage:** 1 and 2 (Task 2; Task 6), 3 (Task 3 test), 4 and 5 (Task 4; Task 6), 6 and 7 (Task 5; Task 6), 8 (Task 1 tests; Task 5), 9 (Task 3; Task 6).
