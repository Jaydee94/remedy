# UI follow-ups, second batch: announcements and focus Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Work off the accessibility items that the follow-ups batch (#119) left open and that need no backend change: a screen reader hears when Remedy answers, diagnoses or asks in an incident's thread, and the keyboard focus does not fall to the page after "Diagnose now" or after an answered question in the thread and on the run page.

**Architecture:** One pure function builds the announcement from the thread's items (`latestAnnouncement` in `thread.ts`, with tests); one small hook (`useFocusRestore`) holds the focus-return logic that `NeedsYouPage` already has inline and that the thread and the run page now share; the thread page renders a visually hidden live region.

**Tech Stack:** React 19, React Router 8, TypeScript 7, Tailwind 4, `node --test`.

**Sources:** the "Left out" list of PR #119 and the memory note `project_ui-conversation-redesign.md` (open items). **Not in this plan** (they need a backend change or a decision): the digest counting a failed re-diagnosis for up to 24 hours (a "diagnosed at" field), `listIncidentRuns` carrying full responder prompts and its cap of 50, `runSteps` recomputed on every event (no evidence of a problem), the Undo button being last in the tab order (the same action, "Stop ignoring", is a button in the panel).

## Global Constraints

- Everything in the repo is English: code, comments, UI copy, docs, commit messages.
- `web/tsconfig.app.json` sets `erasableSyntaxOnly` and `verbatimModuleSyntax`. Pure logic modules import only with relative paths and `.ts` extensions; components may use the `@/` alias.
- **Untrusted text** (diagnosis summaries, agent answers, tool arguments) is only ever React text. An announcement is a template filled with values: a value from an agent is cut and inserted, it never decides what the sentence says.
- `npm run lint` must print no warnings (a `setState` inside an effect body and `Date.now()` in render are warnings in this project).
- Commits end with the line `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>` (exactly this model name).
- In a worktree-isolated session the Bash tool refuses commands that name git in a compound or variable form: plain separate commands or a script file with literal paths; npm with `--prefix web`. Use `command ls`.
- Work on a branch `feat/ui-7-a11y`, never on `main`.

## Review Focus

1. The live region is in the page from the first render and its text changes only when a NEW announce-worthy message arrives; loading the page does not announce the whole history as new, a question that is still running announces nothing, the same kind of message twice in a row is told apart (the text carries a cut of the question or the summary). (Task 1 tests.)
2. After "Diagnose now"/"Diagnose again" and after an answered ask the focus goes to the page heading only when it was lost (never steals it from a field the user is typing in). (Task 2.)
3. The heading that takes the focus is not a control: `tabIndex={-1}`, `outline-none`, no tab stop. It exists once per page (the thread's `h1` is `sr-only` on a phone: it can still take focus). (Task 2.)
4. `NeedsYouPage` behaves exactly as before after the refactor to the shared hook. (Task 2, browser.)

---

## File Structure

| File | Change | Responsibility |
|---|---|---|
| `web/src/thread.ts` + `thread.test.ts` | modify | `latestAnnouncement(items)` |
| `web/src/useFocusRestore.ts` | create | the focus return after a control unmounts |
| `web/src/NeedsYouPage.tsx` | modify | uses the hook (same behaviour) |
| `web/src/IncidentThreadPage.tsx` | modify | the live region, the focus return after diagnose and after an ask |
| `web/src/RunPage.tsx` | modify | the focus return after an ask |

---

### Task 1: The announcement of a thread (logic)

**Files:**
- Modify: `web/src/thread.ts`, `web/src/thread.test.ts`

**Interfaces:**
- Produces: `latestAnnouncement(items: readonly ThreadItem[]): string`.

- [ ] **Step 1: Tests first** (`thread.test.ts`, with the existing fixtures)

`latestAnnouncement` looks at the announce-worthy items from the newest to the oldest and describes the first one; an empty string when there is none:

- an `ask` item: `Remedy asks you: <askText(call).question>` (the table sentence, from `ask.ts`);
- a `diagnosis` item: `Remedy diagnosed this incident: <summary cut to 100 characters>`;
- an `answer` item whose run is `succeeded`: `Remedy answered your question: <the question cut to 60 characters>`; a `failed` run: `Remedy could not answer your question: <the question cut to 60 characters>`; a `queued` or `running` run is not announce-worthy (nothing is said while it works);
- a `working` item (the incident is being diagnosed): `Remedy is looking into this incident.`;
- `event`, `question` and `undiagnosed` items are not announce-worthy.

Cases to test: an empty list and a list of only events/questions give `''`; the newest announce-worthy item wins when several exist (the order of `items` is time order, oldest first: take the last announce-worthy one); a running answer after a diagnosis still announces the diagnosis (the running answer is skipped); two answered questions with different texts give different strings; a summary or a question longer than the cut is cut with an ellipsis and has no line breaks (whitespace collapsed); an `ask` uses the `askText` question.

- [ ] **Step 2: Run them to see them fail** (`npm test --prefix web`).

- [ ] **Step 3: Implement** in `thread.ts`:

```ts
const oneLine = (text: string, max: number) => {
  const flat = text.replace(/\s+/g, ' ').trim()
  return flat.length > max ? `${flat.slice(0, max).trimEnd()}…` : flat
}

/**
 * What a screen reader should be told about a thread: the newest message of Remedy worth announcing, as a sentence. The text changes only
 * when a new such message arrives, so a live region that shows it announces exactly that. A value from an agent is cut and inserted.
 */
export function latestAnnouncement(items: readonly ThreadItem[]): string {
  for (let i = items.length - 1; i >= 0; i--) {
    const item = items[i]
    switch (item.type) {
      case 'ask':
        return `Remedy asks you: ${askText(item.call).question}`
      case 'diagnosis':
        return `Remedy diagnosed this incident: ${oneLine(item.diagnosis.summary, 100)}`
      case 'answer':
        if (item.run.status === 'succeeded') return `Remedy answered your question: ${oneLine(item.run.prompt, 60)}`
        if (item.run.status === 'failed') return `Remedy could not answer your question: ${oneLine(item.run.prompt, 60)}`
        break
      case 'working':
        return 'Remedy is looking into this incident.'
    }
  }
  return ''
}
```

(import `askText` from `./ask.ts`; check that `ask.ts` has only relative `.ts` imports so `thread.ts` stays pure and testable.)

- [ ] **Step 4: Run the tests, lint and build.** Expected: all pass, lint prints nothing.

- [ ] **Step 5: Commit**

```sh
git add web/src
git commit -m "feat(web): the announcement of a thread" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: The live region and the focus return

**Files:**
- Create: `web/src/useFocusRestore.ts`
- Modify: `web/src/NeedsYouPage.tsx`, `web/src/IncidentThreadPage.tsx`, `web/src/RunPage.tsx`

- [ ] **Step 1: The hook** (`web/src/useFocusRestore.ts`)

```ts
import { useCallback, useRef } from 'react'

/**
 * The focus return after the control that had it has gone (a button that was replaced by a message, an ask that was answered): the
 * focus falls to the page, so it goes to the element behind `target` (the page heading) when it is on the page or nowhere. A field
 * the user is typing in keeps it.
 */
export function useFocusRestore<T extends HTMLElement>() {
  const target = useRef<T>(null)
  const restore = useCallback(async () => {
    await new Promise((resolve) => setTimeout(resolve, 50)) // let the page render without the control that went away
    const active = document.activeElement
    if (active === null || active === document.body) target.current?.focus({ preventScroll: true })
  }, [])
  return { target, restore }
}
```

- [ ] **Step 2: `NeedsYouPage`** uses it: `const { target: heading, restore } = useFocusRestore<HTMLHeadingElement>()` replaces its `useRef` and the inline timer in `changed()` (`await reload(); await restore()`); the `h1` keeps `ref={heading} tabIndex={-1}` and `outline-none`. Nothing else changes.

- [ ] **Step 3: The thread** (`IncidentThreadPage.tsx`)

- The incident title `h1` gets `ref={heading}`, `tabIndex={-1}` and `outline-none` (it already has its classes).
- `const { target: heading, restore } = useFocusRestore<HTMLHeadingElement>()` at the top with the other hooks (before any early return).
- After a successful `diagnose()` (after `await reload()`): `void restore()`. After an answered ask: `onChanged={() => void reload().then(restore)}`.
- A live region inside the page container, always rendered once the incident is loaded: `<p className="sr-only" aria-live="polite">{latestAnnouncement(items)}</p>` (`items` is the memo the page already has; render the region after the incident has loaded so that the first text is in place when it appears).

- [ ] **Step 4: The run page** (`RunPage.tsx`): the same hook with the page's `h1` (two `h1 sr-only` occurrences exist for the loading and the loaded state: put the ref and `tabIndex={-1}` `outline-none` on the loaded one) and `onChanged` of its `ApprovalAsk` becomes `() => { refresh(); void restore() }` (check what `refresh` returns and keep its behaviour; the focus return runs after a short delay anyway).

- [ ] **Step 5: Verify and commit.** `npm test --prefix web`, `npm run lint --prefix web`, `npm run build --prefix web`.

```sh
git add web/src
git commit -m "feat(web): announce what Remedy says in a thread, keep the focus after an action" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Verification (run by the controller, in a real browser)

No code changes unless a defect is found. Nothing here is committed.

- [ ] **Step 1: Full checks.** `make check` and `go test -tags webui ./web/` after `make web-build`.
- [ ] **Step 2: Run the app** with a throwaway database (the scripts of the earlier parts: `t6-up.sh` and the live-approval scripts `t4-live.sh`).
- [ ] **Step 3: Check.** In the thread of an incident: the live region text in the DOM (`aria-live="polite"`, `sr-only`), unchanged while a question is running, changes when the answer arrives (insert a succeeded question run and a diagnosis while the page is open and watch the text change within five seconds); "Diagnose now" then the focus is on the `h1` (not on `body`); a live approval answered in the thread then the focus is on the `h1`; typing in the composer while an ask is answered elsewhere keeps the focus in the composer; the same on the run page; `NeedsYouPage` as before (keyboard Yes returns the focus to its `h1`).
- [ ] **Step 4: Clean up** processes, database, `.playwright-mcp/`, stray `*.png` (also in the main checkout).

---

## Self-Review

**Coverage:** announcing new thread messages (Tasks 1, 2), focus after "Diagnose now" and after an answered ask in the thread (Task 2), the same on the run page (Task 2). Not covered, with the reason, at the top.

**Placeholder scan:** none.

**Type consistency:** `latestAnnouncement(items)` (Task 1) takes the `ThreadItem[]` that `buildThread` returns and is used by `IncidentThreadPage` (Task 2); `useFocusRestore<T>()` returns `{ target, restore }` as the three pages use it.

**Review Focus coverage:** 1 (Task 1 tests; Task 3), 2 and 3 (Task 2; Task 3), 4 (Task 2; Task 3).
