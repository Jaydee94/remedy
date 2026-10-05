# UI redesign, part 1: foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The conversation theme (tokens, self-hosted fonts, animations, pill shapes), the new shell (desktop sidebar, phone top bar and tab bar, offline banner, toast), and the new login. The pages that are not rebuilt yet keep working inside the new shell.

**Architecture:** The shadcn semantic tokens in `index.css` take the warm dark palette, so every existing page recolours at once. A new `AppLayout` renders the sidebar (from 768 px up) or a top bar plus a tab bar (below), fed by one polling hook `useShell`. Pages set their top-bar title through a small context. Existing pages sit in a `LegacyPage` route wrapper that later parts remove. The logic that can be wrong without a screen (which section a path belongs to, what a failed poll means, which greeting fits the hour) lives in pure `.ts` modules with relative imports, tested by `node --test`.

**Tech Stack:** React 19, Vite, TypeScript 7 (`erasableSyntaxOnly`, `verbatimModuleSyntax`), Tailwind 4, shadcn/ui, `lucide-react`, `@fontsource-variable/*`, `node --test` for pure logic.

**Spec:** [`docs/specs/2026-10-05-ui-conversation-redesign-design.md`](../specs/2026-10-05-ui-conversation-redesign-design.md), section 4 (and 2, 9, 10).

## Global Constraints

- Everything in the repo is English: code, comments, UI copy, docs, commit messages.
- `web/tsconfig.app.json` sets `erasableSyntaxOnly` and `verbatimModuleSyntax`: no enums or constructor parameter properties, and `import type` for types.
- One dark theme in `:root`; the `.dark` block of `index.css` goes. Fonts are self-hosted through `@fontsource-variable/*` (no request leaves for a font); `@fontsource-variable/geist` is removed. Icons come from `lucide-react`.
- Palette (spec 4.1): background `#1a1612`, card `#231e19`, sidebar `#16130f`, border `#2c2620`, input border `#3a322a`, text `#f1ebe3`, muted text `#a99e91`, subtle text `#74695d`, primary (amber) `#f2c14e` with dark text `#1a1612`, primary hover `#f7d47e`, destructive `#ef6f5e`, success `#7fcf8f`, info `#8cc4ef`, violet `#c3a5f2`, neutral `#8a7f73`; soft state backgrounds open `#3a1f1a`, diagnosing `#3a2f17`, diagnosed `#1e2a33`, resolved `#1f3324`, ignored `#2c2620`.
- Routes do not change. Labels: Today, Conversations, Needs you, Ask Remedy, Setup (short labels on a phone: Today, Chats, Needs you, Ask, Setup).
- Breakpoint `md` (768 px): a 300 px sidebar from there up; below it a 56 px top bar and a bottom tab bar with 44 px targets and the safe-area inset.
- Agent-written text is shown as React text only. Nothing in this part renders HTML from data.
- The animations (`rm-in`, `rm-blink`, `rm-breath`, `rm-pulse`) are switched off by `prefers-reduced-motion`.
- Pure logic modules (`shell.ts`, `greeting.ts`) import only with relative paths and `.ts` extensions (Node cannot resolve the `@/` alias) and contain only erasable TypeScript.
- Commits end with the line `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.
- In a worktree-isolated session the Bash tool refuses commands that name git in a compound or variable form: write such logic into a script file with the Write tool (literal paths) and run it as its own command (see `CLAUDE.md`). A fresh worktree has no `web/node_modules`: run `make web-install` first. Use `command ls`.
- Work on a branch `feat/ui-1-foundation`, never on `main`.

## Review Focus

1. A server without the gatekeeper or the incident routes answers `404` to `/api/approvals` or `/api/incidents`: the shell must stay online ("awake"), not show the offline banner. Only a failure that is not an HTTP answer (the server cannot be reached) means offline. (Task 1 tests `nextShell`; Task 5 checks it in the browser.)
2. The first poll has not answered yet: the sidebar must not say "No open incidents" before it knows. (Task 1 `loaded`; Task 3.)
3. A failed poll keeps the last numbers and incidents instead of showing zero. (Task 1.)
4. A long incident title, many incidents, and a phone width of 390 px: the sidebar list scrolls and truncates, the content never sits under the tab bar, and a legacy page with a wide table scrolls sideways inside its container instead of widening the page. (Task 3, Task 5.)
5. The toast: a second toast replaces the first and restarts the timer, Undo runs once and closes the toast, the timer is cleared when the shell unmounts. (Task 3, Task 5.)
6. Keyboard: every link and button in the shell shows a visible focus ring in the accent colour; the active navigation item says `aria-current="page"`. (Task 3, Task 5.)
7. Login: a wrong password shows the friendly sentence, any other failure shows the API's message, and the greeting follows the hour. (Task 1 tests `greeting`; Task 4.)

---

## File Structure

| File | Change | Responsibility |
|---|---|---|
| `web/package.json`, `web/package-lock.json` | modify | `test` script; fonts replace Geist |
| `web/tsconfig.test.json` | create | type-checks the `*.test.ts` files with Node types |
| `web/tsconfig.json`, `web/tsconfig.app.json` | modify | reference the test config; keep tests out of the app build |
| `Makefile`, `.github/workflows/ci.yml`, `CLAUDE.md` | modify | run and document `npm test` |
| `web/src/shell.ts` + `shell.test.ts` | create | nav items, `sectionOf`, `defaultHeader`, `ShellState`, `nextShell` |
| `web/src/greeting.ts` + `greeting.test.ts` | create | `greeting(hour)` |
| `web/src/index.css`, `web/index.html` | modify | tokens, fonts, animations, base styles; theme colour, favicon |
| `web/src/components/ui/{button,input,textarea,alert,skeleton,switch,card}.tsx` | modify | pill and card shapes |
| `web/src/incidents.ts` | modify | `incidentStateColor` on the new tokens |
| `web/src/components/RemedyMark.tsx`, `RemedyAvatar.tsx` | create | the drop and the avatar |
| `web/src/toast.ts`, `web/src/components/ToastProvider.tsx` | create | toast context and host |
| `web/src/shellContext.ts` | create | page header context and `useShellHeader` |
| `web/src/useShell.ts` | create | the polling hook (replaces `usePendingApprovals.ts`, which is deleted) |
| `web/src/components/shell/{navIcons.ts,PendingBadge.tsx,Sidebar.tsx,MobileBar.tsx,TabBar.tsx,OfflineBanner.tsx}` | create | the shell parts |
| `web/src/components/AppLayout.tsx` | rewrite | assembles the shell |
| `web/src/components/LegacyPage.tsx`, `web/src/App.tsx` | create / modify | wrapper for pages not rebuilt yet |
| `web/src/Login.tsx` | rewrite | the new login |

---

### Task 1: Test setup and the pure logic

**Files:**
- Modify: `web/package.json`, `web/tsconfig.json`, `web/tsconfig.app.json`, `Makefile`, `.github/workflows/ci.yml`, `CLAUDE.md`
- Create: `web/tsconfig.test.json`, `web/src/shell.ts`, `web/src/shell.test.ts`, `web/src/greeting.ts`, `web/src/greeting.test.ts`

**Interfaces:**
- Consumes: the `Incident` type of `web/src/api.ts` (type import only).
- Produces: `navItems: NavItem[]`, `Section`, `sectionOf(pathname)`, `Header`, `defaultHeader(pathname)`, `ShellState`, `emptyShell`, `nextShell(prev, approvals, incidents, isHttpError)`, and `greeting(hour)`; `npm test` in `web/`, `make web-test`.

- [ ] **Step 1: Set up the test runner**

In `web/package.json` add to `scripts` (after `"lint"`): `"test": "node --test 'src/**/*.test.ts'",`.

Create `web/tsconfig.test.json`:

```json
{
  "compilerOptions": {
    "tsBuildInfoFile": "./node_modules/.tmp/tsconfig.test.tsbuildinfo",
    "target": "es2023",
    "lib": ["ES2023", "DOM"],
    "types": ["node"],
    "skipLibCheck": true,

    /* Bundler mode */
    "module": "esnext",
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "verbatimModuleSyntax": true,
    "moduleDetection": "force",
    "noEmit": true,

    /* Linting */
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "erasableSyntaxOnly": true,
    "noFallthroughCasesInSwitch": true
  },
  "include": ["src/**/*.test.ts"]
}
```

In `web/tsconfig.json` add `{ "path": "./tsconfig.test.json" }` to `references`. In `web/tsconfig.app.json` add `"exclude": ["src/**/*.test.ts"]` after `"include": ["src"]`.

In the `Makefile` add `web-test` to `.PHONY`, add

```make
web-test: ## Run the web unit tests (node --test, pure logic only)
	cd web && npm test
```

after `web-lint`, and change the `check` line to `check: fmt vet test web-lint web-test web-build ## Everything CI checks`. In `.github/workflows/ci.yml` add `      - run: npm test` after the `npm run lint` step of the `web` job. In `CLAUDE.md`, in the Commands block after the line `cd web && npm run lint`, add `cd web && npm test                                # node --test over src/**/*.test.ts: pure logic only, no DOM`.

- [ ] **Step 2: Write the failing tests**

Create `web/src/shell.test.ts`:

```ts
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { Incident } from './api.ts'
import { defaultHeader, emptyShell, navItems, nextShell, sectionOf } from './shell.ts'

describe('sectionOf', () => {
  it('maps every navigation path to its section', () => {
    assert.equal(sectionOf('/'), 'today')
    assert.equal(sectionOf('/incidents'), 'conversations')
    assert.equal(sectionOf('/approvals'), 'needs')
    assert.equal(sectionOf('/runs'), 'ask')
    assert.equal(sectionOf('/settings'), 'setup')
  })

  it('keeps a thread under Conversations and a run under Ask Remedy', () => {
    assert.equal(sectionOf('/incidents/27'), 'conversations')
    assert.equal(sectionOf('/runs/3f9c2ab'), 'ask')
  })

  it('does not match a path that only starts with the same letters', () => {
    assert.equal(sectionOf('/incidentsX'), null)
    assert.equal(sectionOf('/runsheet'), null)
    assert.equal(sectionOf('/nope'), null)
  })

  it('has five items, in the order of the navigation', () => {
    assert.deepEqual(navItems.map((n) => n.section), ['today', 'conversations', 'needs', 'ask', 'setup'])
    assert.deepEqual(navItems.map((n) => n.short), ['Today', 'Chats', 'Needs you', 'Ask', 'Setup'])
  })
})

describe('defaultHeader', () => {
  it('uses the label of the section', () => {
    assert.deepEqual(defaultHeader('/'), { title: 'Today' })
    assert.deepEqual(defaultHeader('/approvals'), { title: 'Needs you' })
    assert.deepEqual(defaultHeader('/settings'), { title: 'Setup' })
  })

  it('gives a thread and a run a back target', () => {
    assert.deepEqual(defaultHeader('/incidents/27'), { title: 'Conversation', back: '/incidents' })
    assert.deepEqual(defaultHeader('/runs/abc'), { title: 'Run', back: '/runs' })
  })

  it('falls back to the name of the app for an unknown path', () => {
    assert.deepEqual(defaultHeader('/nope'), { title: 'Remedy' })
  })
})

const incident = (id: number): Incident => ({ id }) as Incident
const ok = <T>(value: T): PromiseFulfilledResult<T> => ({ status: 'fulfilled', value })
const failed = (reason: unknown): PromiseRejectedResult => ({ status: 'rejected', reason })
class HttpError extends Error {}
const isHttpError = (e: unknown) => e instanceof HttpError

describe('nextShell', () => {
  it('starts not loaded, online, with nothing', () => {
    assert.deepEqual(emptyShell, { pending: 0, incidents: [], online: true, loaded: false })
  })

  it('takes both answers and is loaded', () => {
    const got = nextShell(emptyShell, ok([1, 2]), ok([incident(7)]), isHttpError)
    assert.deepEqual(got, { pending: 2, incidents: [incident(7)], online: true, loaded: true })
  })

  it('stays online when a route answers with an HTTP error (the route may not exist)', () => {
    const got = nextShell(emptyShell, failed(new HttpError('404')), ok([incident(7)]), isHttpError)
    assert.equal(got.online, true)
    assert.equal(got.pending, 0)
    assert.equal(got.loaded, true)
  })

  it('goes offline when the server cannot be reached, and keeps the last data', () => {
    const before = nextShell(emptyShell, ok([1, 2, 3]), ok([incident(1), incident(2)]), isHttpError)
    const got = nextShell(before, failed(new TypeError('fetch failed')), failed(new TypeError('fetch failed')), isHttpError)
    assert.equal(got.online, false)
    assert.equal(got.pending, 3)
    assert.deepEqual(got.incidents, [incident(1), incident(2)])
    assert.equal(got.loaded, true)
  })

  it('is not loaded while the incidents have never answered', () => {
    const got = nextShell(emptyShell, ok([]), failed(new TypeError('fetch failed')), isHttpError)
    assert.equal(got.loaded, false)
    assert.equal(got.online, false)
  })

  it('comes back online when the server answers again', () => {
    const down = nextShell(emptyShell, failed(new TypeError('x')), failed(new TypeError('x')), isHttpError)
    const up = nextShell(down, ok([1]), ok([]), isHttpError)
    assert.equal(up.online, true)
    assert.equal(up.pending, 1)
  })

  it('keeps the previous count when a route answers with a transient HTTP error', () => {
    const before = nextShell(emptyShell, ok([1, 2]), ok([]), isHttpError)
    const got = nextShell(before, failed(new HttpError('500')), ok([]), isHttpError)
    assert.equal(got.pending, 2)
    assert.equal(got.online, true)
  })
})
```

Create `web/src/greeting.test.ts`:

```ts
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { greeting } from './greeting.ts'

describe('greeting', () => {
  it('says good morning from five to eleven', () => {
    assert.equal(greeting(5), 'Good morning')
    assert.equal(greeting(11), 'Good morning')
  })

  it('says good afternoon from noon to five in the afternoon', () => {
    assert.equal(greeting(12), 'Good afternoon')
    assert.equal(greeting(17), 'Good afternoon')
  })

  it('says good evening at the evening and through the night', () => {
    assert.equal(greeting(18), 'Good evening')
    assert.equal(greeting(23), 'Good evening')
    assert.equal(greeting(0), 'Good evening')
    assert.equal(greeting(4), 'Good evening')
  })
})
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd web && npm test`
Expected: FAIL: `Cannot find module './shell.ts'` and `'./greeting.ts'` (also confirms that `node --test` runs `.ts` files; if Node refuses to run them, report the Node version and the message instead of changing the approach).

- [ ] **Step 4: Implement the pure modules**

Create `web/src/greeting.ts`:

```ts
/** The greeting of the login for an hour of the day (0 to 23). */
export function greeting(hour: number): string {
  if (hour >= 5 && hour < 12) return 'Good morning'
  if (hour >= 12 && hour < 18) return 'Good afternoon'
  return 'Good evening'
}
```

Create `web/src/shell.ts`:

```ts
import type { Incident } from './api.ts'

export type Section = 'today' | 'conversations' | 'needs' | 'ask' | 'setup'

export interface NavItem {
  section: Section
  to: string
  label: string
  /** The label on a phone, where the tab bar is narrow. */
  short: string
}

export const navItems: NavItem[] = [
  { section: 'today', to: '/', label: 'Today', short: 'Today' },
  { section: 'conversations', to: '/incidents', label: 'Conversations', short: 'Chats' },
  { section: 'needs', to: '/approvals', label: 'Needs you', short: 'Needs you' },
  { section: 'ask', to: '/runs', label: 'Ask Remedy', short: 'Ask' },
  { section: 'setup', to: '/settings', label: 'Setup', short: 'Setup' },
]

/** The section a path belongs to: a thread belongs to Conversations, a run to Ask Remedy. Null for a path outside the navigation. */
export function sectionOf(pathname: string): Section | null {
  if (pathname === '/') return 'today'
  for (const item of navItems.slice(1)) {
    if (pathname === item.to || pathname.startsWith(`${item.to}/`)) return item.section
  }
  return null
}

export interface Header {
  title: string
  /** Where the back arrow of the phone's top bar goes. */
  back?: string
}

/** The title and back target of the phone's top bar for a path, until a page sets its own. */
export function defaultHeader(pathname: string): Header {
  if (pathname.startsWith('/incidents/')) return { title: 'Conversation', back: '/incidents' }
  if (pathname.startsWith('/runs/')) return { title: 'Run', back: '/runs' }
  const section = sectionOf(pathname)
  const item = navItems.find((n) => n.section === section)
  return { title: item ? item.label : 'Remedy' }
}

/** What the shell shows: calls that wait for a decision, the active incidents, whether the server answers, whether it has answered once. */
export interface ShellState {
  pending: number
  incidents: Incident[]
  online: boolean
  loaded: boolean
}

export const emptyShell: ShellState = { pending: 0, incidents: [], online: true, loaded: false }

/**
 * The state after one poll. A rejection that is an HTTP answer (the route may not exist on this server, or it failed once) leaves
 * the shell online and keeps what it had for that part; any other rejection means the server cannot be reached: the shell goes
 * offline and also keeps what it had. `loaded` turns true when the incidents have answered once.
 */
export function nextShell(
  prev: ShellState,
  approvals: PromiseSettledResult<readonly unknown[]>,
  incidents: PromiseSettledResult<Incident[]>,
  isHttpError: (reason: unknown) => boolean,
): ShellState {
  const unreachable = [approvals, incidents].some((r) => r.status === 'rejected' && !isHttpError(r.reason))
  return {
    pending: approvals.status === 'fulfilled' ? approvals.value.length : prev.pending,
    incidents: incidents.status === 'fulfilled' ? incidents.value : prev.incidents,
    online: !unreachable,
    loaded: prev.loaded || incidents.status === 'fulfilled',
  }
}
```

- [ ] **Step 5: Run the tests and the checks**

Run: `cd web && npm test`
Expected: PASS: 14 tests in `shell.test.ts` and 3 in `greeting.test.ts`, pristine output.
Run: `cd web && npm run lint && npm run build`
Expected: both pass (the build now also type-checks the test files through `tsconfig.test.json`).

- [ ] **Step 6: Commit**

```sh
git add web/package.json web/tsconfig.json web/tsconfig.app.json web/tsconfig.test.json web/src/shell.ts web/src/shell.test.ts web/src/greeting.ts web/src/greeting.test.ts Makefile .github/workflows/ci.yml CLAUDE.md
git commit -m "feat(web): add node --test for pure logic, the shell state logic and the login greeting" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Theme, fonts and shapes

**Files:**
- Modify: `web/package.json`, `web/package-lock.json`, `web/src/index.css`, `web/index.html`, `web/src/components/ui/button.tsx`, `input.tsx`, `textarea.tsx`, `alert.tsx`, `skeleton.tsx`, `switch.tsx`, `card.tsx`, `web/src/incidents.ts`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: Tailwind colour utilities `bg-card`, `bg-sidebar`, `text-subtle`, `text-success`, `text-info`, `text-violet`, `bg-neutral`, `bg-soft-open|diagnosing|diagnosed|resolved|ignored`, `bg-primary-hover`; font utilities `font-sans`, `font-serif`, `font-mono`; animation utilities `animate-rm-in`, `animate-rm-blink`, `animate-rm-breath`, `animate-rm-pulse`; `incidentStateColor` returning token classes (`bg-destructive`, `bg-primary`, `bg-info`, `bg-success`, `bg-neutral`).

There is no web test runner for visuals: this task is verified by build, lint and the browser check of Task 5. Do the steps in order.

- [ ] **Step 1: Swap the fonts**

Run: `cd web && npm uninstall @fontsource-variable/geist && npm install @fontsource-variable/literata @fontsource-variable/schibsted-grotesk @fontsource-variable/jetbrains-mono`
Expected: `package.json` lists the three new packages and no Geist; the lock file changes. Check with `grep -rn "geist" web/src web/index.html` that nothing else names Geist (only `index.css` does, rewritten next).

- [ ] **Step 2: Rewrite `web/src/index.css`**

First run `grep -rn "chart-" web/src` and `grep -rn "sidebar" web/src`: keep any token a file uses. Then replace the file with:

```css
@import "tailwindcss";
@import "tw-animate-css";
@import "shadcn/tailwind.css";
@import "@fontsource-variable/schibsted-grotesk";
@import "@fontsource-variable/literata";
@import "@fontsource-variable/jetbrains-mono";

@custom-variant dark (&:is(.dark *));

:root {
  color-scheme: dark;
  --background: #1a1612;
  --foreground: #f1ebe3;
  --card: #231e19;
  --card-foreground: #f1ebe3;
  --popover: #231e19;
  --popover-foreground: #f1ebe3;
  --primary: #f2c14e;
  --primary-hover: #f7d47e;
  --primary-foreground: #1a1612;
  --secondary: #2c2620;
  --secondary-foreground: #e6ded3;
  --muted: #2c2620;
  --muted-foreground: #a99e91;
  --accent: #2c2620;
  --accent-foreground: #f1ebe3;
  --destructive: #ef6f5e;
  --border: #2c2620;
  --input: #3a322a;
  --ring: #f2c14e;
  --radius: 1rem;

  --sidebar: #16130f;
  --sidebar-foreground: #f1ebe3;
  --sidebar-primary: #f2c14e;
  --sidebar-primary-foreground: #1a1612;
  --sidebar-accent: #2c2620;
  --sidebar-accent-foreground: #f1ebe3;
  --sidebar-border: #2c2620;
  --sidebar-ring: #f2c14e;

  --success: #7fcf8f;
  --info: #8cc4ef;
  --violet: #c3a5f2;
  --subtle: #74695d;
  --neutral: #8a7f73;
  --soft-open: #3a1f1a;
  --soft-diagnosing: #3a2f17;
  --soft-diagnosed: #1e2a33;
  --soft-resolved: #1f3324;
  --soft-ignored: #2c2620;
}

@theme inline {
  --font-sans: "Schibsted Grotesk Variable", ui-sans-serif, system-ui, sans-serif;
  --font-serif: "Literata Variable", ui-serif, Georgia, serif;
  --font-mono: "JetBrains Mono Variable", ui-monospace, monospace;
  --font-heading: var(--font-sans);

  --color-sidebar-ring: var(--sidebar-ring);
  --color-sidebar-border: var(--sidebar-border);
  --color-sidebar-accent-foreground: var(--sidebar-accent-foreground);
  --color-sidebar-accent: var(--sidebar-accent);
  --color-sidebar-primary-foreground: var(--sidebar-primary-foreground);
  --color-sidebar-primary: var(--sidebar-primary);
  --color-sidebar-foreground: var(--sidebar-foreground);
  --color-sidebar: var(--sidebar);
  --color-ring: var(--ring);
  --color-input: var(--input);
  --color-border: var(--border);
  --color-destructive: var(--destructive);
  --color-accent-foreground: var(--accent-foreground);
  --color-accent: var(--accent);
  --color-muted-foreground: var(--muted-foreground);
  --color-muted: var(--muted);
  --color-secondary-foreground: var(--secondary-foreground);
  --color-secondary: var(--secondary);
  --color-primary-foreground: var(--primary-foreground);
  --color-primary-hover: var(--primary-hover);
  --color-primary: var(--primary);
  --color-popover-foreground: var(--popover-foreground);
  --color-popover: var(--popover);
  --color-card-foreground: var(--card-foreground);
  --color-card: var(--card);
  --color-foreground: var(--foreground);
  --color-background: var(--background);

  --color-success: var(--success);
  --color-info: var(--info);
  --color-violet: var(--violet);
  --color-subtle: var(--subtle);
  --color-neutral: var(--neutral);
  --color-soft-open: var(--soft-open);
  --color-soft-diagnosing: var(--soft-diagnosing);
  --color-soft-diagnosed: var(--soft-diagnosed);
  --color-soft-resolved: var(--soft-resolved);
  --color-soft-ignored: var(--soft-ignored);

  --radius-sm: calc(var(--radius) * 0.6);
  --radius-md: calc(var(--radius) * 0.8);
  --radius-lg: var(--radius);
  --radius-xl: calc(var(--radius) * 1.4);
  --radius-2xl: calc(var(--radius) * 1.8);
  --radius-3xl: calc(var(--radius) * 2.2);
  --radius-4xl: calc(var(--radius) * 2.6);

  --animate-rm-in: rm-in 0.35s ease-out both;
  --animate-rm-blink: rm-blink 1.2s ease-in-out infinite;
  --animate-rm-breath: rm-breath 1.8s ease-in-out infinite;
  --animate-rm-pulse: rm-pulse 1.4s ease-in-out infinite;
}

@keyframes rm-in {
  from {
    opacity: 0;
    transform: translateY(6px);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

@keyframes rm-blink {
  0%,
  80%,
  100% {
    opacity: 0.25;
  }
  40% {
    opacity: 1;
  }
}

@keyframes rm-breath {
  0%,
  100% {
    box-shadow: 0 0 0 2px var(--background), 0 0 0 4px rgb(242 193 78 / 0.15);
  }
  50% {
    box-shadow: 0 0 0 2px var(--background), 0 0 0 6px rgb(242 193 78 / 0.5);
  }
}

@keyframes rm-pulse {
  0%,
  100% {
    opacity: 0.5;
  }
  50% {
    opacity: 1;
  }
}

@layer base {
  * {
    @apply border-border outline-ring/50;
    scrollbar-width: thin;
    scrollbar-color: var(--input) transparent;
  }
  body {
    @apply bg-background font-sans text-sm text-foreground antialiased;
  }
  html {
    @apply font-sans;
  }
  @media (prefers-reduced-motion: reduce) {
    *,
    *::before,
    *::after {
      animation-duration: 0.01ms !important;
      animation-iteration-count: 1 !important;
      transition-duration: 0.01ms !important;
    }
  }
}
```

If the grep of Step 2 found a use of a `--chart-*` token, keep that token in `:root` and `@theme inline` with its old value.

- [ ] **Step 3: Update `web/index.html`**

Replace its content with:

```html
<!doctype html>
<html lang="en" class="dark">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0, viewport-fit=cover" />
    <meta name="color-scheme" content="dark" />
    <meta name="theme-color" content="#1a1612" />
    <link
      rel="icon"
      href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24'%3E%3Crect width='24' height='24' rx='6' fill='%231a1612'/%3E%3Cpath d='M12 4.2c-.3.3-5.6 6.6-5.6 10.3a5.6 5.6 0 0 0 11.2 0c0-3.7-5.3-10-5.6-10.3z' fill='%23f2c14e'/%3E%3C/svg%3E"
    />
    <title>Remedy</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

(`viewport-fit=cover` makes `env(safe-area-inset-bottom)` work for the tab bar.)

- [ ] **Step 4: Reshape the shadcn components**

`web/src/components/ui/button.tsx`: in the `buttonVariants` base string replace `rounded-lg` with `rounded-full`. Replace the `variants` block with:

```ts
    variants: {
      variant: {
        default: "bg-primary font-semibold text-primary-foreground hover:bg-primary-hover",
        outline:
          "border-input bg-transparent hover:bg-card hover:text-foreground aria-expanded:bg-card aria-expanded:text-foreground",
        secondary:
          "bg-secondary text-secondary-foreground hover:bg-[color-mix(in_oklch,var(--secondary),var(--foreground)_5%)] aria-expanded:bg-secondary aria-expanded:text-secondary-foreground",
        ghost:
          "hover:bg-muted hover:text-foreground aria-expanded:bg-muted aria-expanded:text-foreground",
        destructive:
          "bg-destructive font-semibold text-background hover:bg-destructive/90 focus-visible:ring-destructive/40",
        link: "text-primary underline-offset-4 hover:underline",
      },
      size: {
        default:
          "h-10 gap-2 px-4 has-data-[icon=inline-end]:pr-3 has-data-[icon=inline-start]:pl-3",
        xs: "h-6 gap-1 px-2.5 text-xs has-data-[icon=inline-end]:pr-1.5 has-data-[icon=inline-start]:pl-1.5 [&_svg:not([class*='size-'])]:size-3",
        sm: "h-8 gap-1.5 px-3.5 text-[0.8rem] has-data-[icon=inline-end]:pr-2.5 has-data-[icon=inline-start]:pl-2.5 [&_svg:not([class*='size-'])]:size-3.5",
        lg: "h-12 gap-2 px-5 text-[15px] has-data-[icon=inline-end]:pr-4 has-data-[icon=inline-start]:pl-4",
        icon: "size-10",
        "icon-xs": "size-6 [&_svg:not([class*='size-'])]:size-3",
        "icon-sm": "size-8",
        "icon-lg": "size-12",
      },
    },
```

`input.tsx`: in the `cn(...)` string replace `h-8` with `h-11`, `rounded-lg` with `rounded-full`, `bg-transparent` with `bg-card`, `px-2.5` with `px-5`, and delete the two tokens `dark:bg-input/30` and `dark:disabled:bg-input/80`.

`textarea.tsx`: replace `rounded-lg` with `rounded-2xl`, `bg-transparent` with `bg-card`, `px-2.5 py-2` with `px-5 py-3`, and delete `dark:bg-input/30` and `dark:disabled:bg-input/80`.

`alert.tsx`: in `alertVariants` base string replace `rounded-lg` with `rounded-2xl` and `px-2.5 py-2` with `px-4 py-3`.

`skeleton.tsx`: replace `animate-pulse rounded-md bg-muted` with `animate-rm-pulse rounded-2xl bg-card`.

`card.tsx`: in `Card` replace `ring-foreground/10` with `ring-border`.

`switch.tsx`: in the root `cn(...)` string replace `data-[size=default]:h-[18.4px] data-[size=default]:w-[32px]` with `data-[size=default]:h-6.5 data-[size=default]:w-11`, replace `data-unchecked:bg-input dark:data-unchecked:bg-input/80` with `data-unchecked:bg-input`; replace the thumb's class string with:

```
"pointer-events-none block rounded-full ring-0 transition-transform group-data-[size=default]/switch:size-5 group-data-[size=sm]/switch:size-3 group-data-[size=default]/switch:data-checked:translate-x-full group-data-[size=sm]/switch:data-checked:translate-x-[calc(100%-2px)] group-data-[size=default]/switch:data-unchecked:translate-x-0.5 group-data-[size=sm]/switch:data-unchecked:translate-x-0 data-checked:bg-primary-foreground data-unchecked:bg-muted-foreground"
```

- [ ] **Step 5: Move the state colours to the tokens**

In `web/src/incidents.ts` replace `incidentStateColor` with:

```ts
export const incidentStateColor: Record<IncidentState, string> = {
  open: 'bg-destructive',
  diagnosing: 'bg-primary',
  diagnosed: 'bg-info',
  resolved: 'bg-success',
  ignored: 'bg-neutral',
}
```

- [ ] **Step 6: Verify**

Run: `cd web && npm run lint && npm run build && npm test`
Expected: all pass. Run `grep -n "Geist\|geist" web/src web/package.json`: no output.
Start `make dev-web` is not needed here; the visual check is Task 5.

- [ ] **Step 7: Commit**

```sh
git add web/package.json web/package-lock.json web/index.html web/src/index.css web/src/incidents.ts web/src/components/ui
git commit -m "feat(web): the warm dark theme, self-hosted fonts and pill shapes" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The shell

**Files:**
- Create: `web/src/components/RemedyMark.tsx`, `web/src/components/RemedyAvatar.tsx`, `web/src/toast.ts`, `web/src/components/ToastProvider.tsx`, `web/src/shellContext.ts`, `web/src/useShell.ts`, `web/src/components/shell/navIcons.ts`, `web/src/components/shell/PendingBadge.tsx`, `web/src/components/shell/Sidebar.tsx`, `web/src/components/shell/MobileBar.tsx`, `web/src/components/shell/TabBar.tsx`, `web/src/components/shell/OfflineBanner.tsx`, `web/src/components/LegacyPage.tsx`
- Modify: `web/src/components/AppLayout.tsx` (rewrite), `web/src/App.tsx`
- Delete: `web/src/usePendingApprovals.ts`

**Interfaces:**
- Consumes: from Task 1 `navItems`, `Section`, `sectionOf`, `defaultHeader`, `Header`, `ShellState`, `emptyShell`, `nextShell`; from Task 2 the tokens and animations; `api.listApprovals('pending')`, `api.listIncidents('active')`, `ApiError`, `timeAgo`, `incidentStateColor`, `cn`.
- Produces: `RemedyMark({size?, cutout?, className?})`, `RemedyAvatar({kind})` with `kind: 'idle' | 'working' | 'ask'`; `useToast(): { show(text, undo?) }`, `ToastProvider`; `useShellHeader(header | null)`, `ShellContext`; `useShell(): ShellState`; `LegacyPage`; the new `AppLayout({onSignOut})`.

No unit test fits here (DOM, no runner); the pure parts are tested in Task 1. Verification is lint, build and Task 5.

- [ ] **Step 1: The mark and the avatar**

`web/src/components/RemedyMark.tsx`:

```tsx
interface Props {
  size?: number
  /** The colour of the highlight cut into the drop: the surface the mark sits on. */
  cutout?: string
  className?: string
}

/** The drop of Remedy. */
export default function RemedyMark({ size = 24, cutout = 'var(--background)', className }: Props) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} className={className} aria-hidden>
      <path d="M12 2.6c-.3.4-7 8.3-7 12.9a7 7 0 0 0 14 0c0-4.6-6.7-12.5-7-12.9z" fill="var(--primary)" />
      <path d="M8.7 15.4a3.3 3.3 0 0 0 3.3 3.3" stroke={cutout} strokeWidth="1.5" fill="none" strokeLinecap="round" />
    </svg>
  )
}
```

`web/src/components/RemedyAvatar.tsx`:

```tsx
import { cn } from '@/lib/utils'

interface Props {
  /** idle: Remedy speaks; working: it is busy (a breathing ring); ask: it waits for an answer. */
  kind?: 'idle' | 'working' | 'ask'
}

/** Remedy's avatar in a conversation. */
export default function RemedyAvatar({ kind = 'idle' }: Props) {
  const ask = kind === 'ask'
  return (
    <span
      aria-hidden
      className={cn(
        'flex size-9 shrink-0 items-center justify-center rounded-full',
        ask ? 'bg-primary' : 'bg-soft-diagnosing',
        kind === 'working' && 'animate-rm-breath',
      )}
    >
      <svg viewBox="0 0 24 24" width={21} height={21}>
        <path
          d="M12 2.6c-.3.4-7 8.3-7 12.9a7 7 0 0 0 14 0c0-4.6-6.7-12.5-7-12.9z"
          fill={ask ? 'var(--primary-foreground)' : 'var(--primary)'}
        />
        <path
          d="M8.7 15.4a3.3 3.3 0 0 0 3.3 3.3"
          stroke={ask ? 'var(--primary)' : 'var(--soft-diagnosing)'}
          strokeWidth="1.6"
          fill="none"
          strokeLinecap="round"
        />
      </svg>
    </span>
  )
}
```

- [ ] **Step 2: The toast**

`web/src/toast.ts`:

```ts
import { createContext, useContext } from 'react'

export interface ToastApi {
  /** Shows a toast for a few seconds. With `undo` it carries an Undo button; a new toast replaces the one that is showing. */
  show: (text: string, undo?: () => void) => void
}

export const ToastContext = createContext<ToastApi>({ show: () => {} })

export function useToast(): ToastApi {
  return useContext(ToastContext)
}
```

`web/src/components/ToastProvider.tsx`:

```tsx
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { ToastContext } from '@/toast.ts'

const SHOWN_MS = 4200

interface Toast {
  id: number
  text: string
  undo?: () => void
}

/** Hosts the toast of the shell. It sits above the tab bar on a phone. */
export default function ToastProvider({ children }: { children: ReactNode }) {
  const [toast, setToast] = useState<Toast | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const lastId = useRef(0)

  const show = useCallback((text: string, undo?: () => void) => {
    clearTimeout(timer.current)
    lastId.current += 1
    setToast({ id: lastId.current, text, undo })
    timer.current = setTimeout(() => setToast(null), SHOWN_MS)
  }, [])

  const dismiss = useCallback(() => {
    clearTimeout(timer.current)
    setToast(null)
  }, [])

  useEffect(() => () => clearTimeout(timer.current), [])

  const value = useMemo(() => ({ show }), [show])

  return (
    <ToastContext.Provider value={value}>
      {children}
      {toast && (
        <div
          key={toast.id}
          role="status"
          className="fixed bottom-24 left-1/2 z-50 flex max-w-[calc(100%-2rem)] -translate-x-1/2 animate-rm-in items-center gap-3.5 rounded-full bg-foreground py-2.5 pr-2.5 pl-4.5 text-background shadow-2xl md:bottom-7"
        >
          <span className="font-semibold">{toast.text}</span>
          {toast.undo && (
            <button
              type="button"
              onClick={() => {
                toast.undo?.()
                dismiss()
              }}
              className="flex h-8 items-center rounded-full bg-background px-3.5 font-semibold text-primary outline-none focus-visible:ring-3 focus-visible:ring-primary/50"
            >
              Undo
            </button>
          )}
        </div>
      )}
    </ToastContext.Provider>
  )
}
```

- [ ] **Step 3: The page header context and the polling hook**

`web/src/shellContext.ts`:

```ts
import { createContext, useContext, useEffect } from 'react'
import type { Header } from './shell.ts'

export interface ShellApi {
  setHeader: (header: Header | null) => void
}

export const ShellContext = createContext<ShellApi>({ setHeader: () => {} })

/** A page sets the title and back target of the phone's top bar for as long as it is mounted. Null keeps the default of the route. */
export function useShellHeader(header: Header | null) {
  const { setHeader } = useContext(ShellContext)
  const title = header?.title
  const back = header?.back
  useEffect(() => {
    setHeader(title === undefined ? null : { title, back })
    return () => setHeader(null)
  }, [setHeader, title, back])
}
```

`web/src/useShell.ts`:

```ts
import { useEffect, useState } from 'react'
import { api, ApiError } from './api.ts'
import { emptyShell, nextShell } from './shell.ts'
import type { ShellState } from './shell.ts'

const POLL_MS = 2000 // a call that waits for a decision must show within two seconds (phase 2 spec, success criteria)

/** What the shell shows, from one poll every two seconds. A failed poll keeps the last data; see nextShell for what counts as offline. */
export function useShell(): ShellState {
  const [state, setState] = useState<ShellState>(emptyShell)

  useEffect(() => {
    let active = true
    const load = async () => {
      const [approvals, incidents] = await Promise.allSettled([api.listApprovals('pending'), api.listIncidents('active')])
      if (active) setState((prev) => nextShell(prev, approvals, incidents, (e) => e instanceof ApiError))
    }
    void load()
    const timer = setInterval(() => void load(), POLL_MS)
    return () => {
      active = false
      clearInterval(timer)
    }
  }, [])

  return state
}
```

Delete `web/src/usePendingApprovals.ts`.

- [ ] **Step 4: The shell parts**

`web/src/components/shell/navIcons.ts`:

```ts
import { Hand, MessagesSquare, Newspaper, SlidersHorizontal, SquarePen } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import type { Section } from '@/shell.ts'

export const navIcons: Record<Section, LucideIcon> = {
  today: Newspaper,
  conversations: MessagesSquare,
  needs: Hand,
  ask: SquarePen,
  setup: SlidersHorizontal,
}
```

`web/src/components/shell/PendingBadge.tsx`:

```tsx
import { cn } from '@/lib/utils'

/** The number of calls that wait for a decision. */
export default function PendingBadge({ count, className }: { count: number; className?: string }) {
  return (
    <span
      aria-label={`${count} waiting for a decision`}
      className={cn(
        'flex h-5.5 min-w-5.5 items-center justify-center rounded-full bg-primary px-1.5 text-xs font-bold text-primary-foreground',
        className,
      )}
    >
      {count}
    </span>
  )
}
```

`web/src/components/shell/OfflineBanner.tsx`:

```tsx
/** Shown while the server cannot be reached. */
export default function OfflineBanner() {
  return (
    <div
      role="status"
      className="flex shrink-0 items-center justify-center gap-2.5 bg-soft-diagnosing px-4 py-2 text-center text-[13px] font-semibold text-primary"
    >
      <span aria-hidden className="size-1.75 shrink-0 rounded-full bg-primary" />
      Reconnecting… live updates are paused. What you see may be a minute old.
    </div>
  )
}
```

`web/src/components/shell/Sidebar.tsx`:

```tsx
import { Link } from 'react-router'
import { incidentStateColor, timeAgo } from '@/incidents.ts'
import { navItems } from '@/shell.ts'
import type { Section, ShellState } from '@/shell.ts'
import RemedyMark from '@/components/RemedyMark'
import { navIcons } from '@/components/shell/navIcons.ts'
import PendingBadge from '@/components/shell/PendingBadge'
import { cn } from '@/lib/utils'

interface Props {
  section: Section | null
  pathname: string
  shell: ShellState
  onSignOut: () => void
}

const focus = 'outline-none focus-visible:ring-3 focus-visible:ring-ring/50'

/** The desktop sidebar: the navigation, the open conversations, what is coming later. Hidden below the md breakpoint. */
export default function Sidebar({ section, pathname, shell, onSignOut }: Props) {
  return (
    <aside className="hidden w-75 shrink-0 flex-col border-r border-border bg-sidebar md:flex">
      <Link to="/" className={cn('flex items-center gap-2.5 rounded-full px-5.5 pt-5 pb-4 text-foreground', focus)}>
        <RemedyMark size={26} cutout="var(--sidebar)" />
        <span className="font-serif text-[21px] font-medium tracking-tight">remedy</span>
        <span className="ml-auto flex items-center gap-1.5 text-xs text-muted-foreground">
          <span aria-hidden className={cn('size-1.75 rounded-full', shell.online ? 'bg-success' : 'bg-primary')} />
          {shell.online ? 'awake' : 'dozing'}
        </span>
      </Link>

      <nav aria-label="Main" className="flex flex-col gap-0.5 px-2.5">
        {navItems.map((item) => {
          const Icon = navIcons[item.section]
          const active = item.section === section
          return (
            <Link
              key={item.to}
              to={item.to}
              aria-current={active ? 'page' : undefined}
              className={cn(
                'flex h-10 items-center gap-3 rounded-full px-3 transition-colors',
                active ? 'bg-secondary text-foreground' : 'text-muted-foreground hover:bg-card hover:text-foreground',
                focus,
              )}
            >
              <Icon className="size-4.25" aria-hidden />
              <span className="flex-1">{item.label}</span>
              {item.section === 'needs' && shell.pending > 0 && <PendingBadge count={shell.pending} />}
            </Link>
          )
        })}
      </nav>

      <div className="mx-5.5 mt-5.5 mb-2 text-xs text-subtle">Open conversations</div>
      <div className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-auto px-2.5">
        {shell.incidents.map((incident) => {
          const here = pathname === `/incidents/${incident.id}`
          return (
            <Link
              key={incident.id}
              to={`/incidents/${incident.id}`}
              aria-current={here ? 'page' : undefined}
              className={cn('flex gap-2.5 rounded-2xl px-3 py-2.5 transition-colors hover:bg-card', here && 'bg-card', focus)}
            >
              <span aria-hidden className={cn('mt-1.5 size-2 shrink-0 rounded-full', incidentStateColor[incident.state])} />
              <span className="flex min-w-0 flex-1 gap-2">
                <span className="flex-1 truncate font-semibold">{incident.title}</span>
                <span className="shrink-0 text-xs text-subtle">{timeAgo(incident.lastSeen)}</span>
              </span>
            </Link>
          )
        })}
        {shell.loaded && shell.incidents.length === 0 && (
          <span className="px-3 py-2.5 text-[13px] text-subtle">No open incidents. I'll start one when a check fails.</span>
        )}
      </div>

      <div className="flex flex-col gap-2 border-t border-border px-5.5 py-3.5">
        <span className="text-xs text-subtle">Coming later</span>
        <span className="flex flex-wrap gap-1.5">
          {['Pull requests', 'Knowledge', 'Graph'].map((name) => (
            <span key={name} className="rounded-full border border-dashed border-input px-2.5 py-0.5 text-xs text-subtle">
              {name}
            </span>
          ))}
        </span>
        <button
          type="button"
          onClick={onSignOut}
          className={cn('mt-1 self-start rounded-full text-[13px] text-subtle transition-colors hover:text-foreground', focus)}
        >
          Sign out
        </button>
      </div>
    </aside>
  )
}
```

`web/src/components/shell/MobileBar.tsx`:

```tsx
import { ChevronLeft } from 'lucide-react'
import { Link } from 'react-router'
import type { Header } from '@/shell.ts'
import RemedyMark from '@/components/RemedyMark'
import { cn } from '@/lib/utils'

/** The top bar of a phone: back, the mark, the title, and whether Remedy is awake. Hidden from the md breakpoint up. */
export default function MobileBar({ header, online }: { header: Header; online: boolean }) {
  return (
    <div className="flex h-14 shrink-0 items-center gap-2.5 border-b border-border px-4 md:hidden">
      {header.back && (
        <Link
          to={header.back}
          aria-label="Back"
          className="-ml-3 flex size-11 items-center justify-center rounded-full text-foreground outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          <ChevronLeft className="size-5.5" aria-hidden />
        </Link>
      )}
      <RemedyMark size={24} />
      <h1 className="min-w-0 flex-1 truncate font-serif text-[17px] font-medium">{header.title}</h1>
      <span aria-hidden className={cn('size-2 rounded-full', online ? 'bg-success' : 'bg-primary')} />
      <span className="sr-only">{online ? 'Remedy is awake' : 'Remedy is dozing'}</span>
    </div>
  )
}
```

`web/src/components/shell/TabBar.tsx`:

```tsx
import { Link } from 'react-router'
import { navItems } from '@/shell.ts'
import type { Section } from '@/shell.ts'
import { navIcons } from '@/components/shell/navIcons.ts'
import PendingBadge from '@/components/shell/PendingBadge'
import { cn } from '@/lib/utils'

/** The bottom tab bar of a phone. 44 px or more per target, and the safe-area inset under it. Hidden from the md breakpoint up. */
export default function TabBar({ section, pending }: { section: Section | null; pending: number }) {
  return (
    <nav
      aria-label="Main"
      className="flex shrink-0 border-t border-border bg-sidebar px-1.5 pt-1.5 pb-[max(0.75rem,env(safe-area-inset-bottom))] md:hidden"
    >
      {navItems.map((item) => {
        const Icon = navIcons[item.section]
        const active = item.section === section
        return (
          <Link
            key={item.to}
            to={item.to}
            aria-current={active ? 'page' : undefined}
            className={cn(
              'relative flex h-13.5 flex-1 flex-col items-center justify-center gap-1 rounded-2xl text-[11px] outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
              active ? 'text-primary' : 'text-muted-foreground',
            )}
          >
            <Icon className="size-5.5" aria-hidden />
            {item.short}
            {item.section === 'needs' && pending > 0 && (
              <PendingBadge count={pending} className="absolute top-1 left-[calc(50%+6px)] h-4.5 min-w-4.5 px-1 text-[11px]" />
            )}
          </Link>
        )
      })}
    </nav>
  )
}
```

- [ ] **Step 5: The layout, the legacy wrapper and the routes**

Rewrite `web/src/components/AppLayout.tsx`:

```tsx
import { useMemo, useState } from 'react'
import { Outlet, useLocation } from 'react-router'
import { defaultHeader, sectionOf } from '@/shell.ts'
import type { Header } from '@/shell.ts'
import { ShellContext } from '@/shellContext.ts'
import { useShell } from '@/useShell.ts'
import MobileBar from '@/components/shell/MobileBar'
import OfflineBanner from '@/components/shell/OfflineBanner'
import Sidebar from '@/components/shell/Sidebar'
import TabBar from '@/components/shell/TabBar'
import ToastProvider from '@/components/ToastProvider'

/** The shell: sidebar on a desktop, top bar and tab bar on a phone, an offline banner, and the toast. Only the content scrolls. */
export default function AppLayout({ onSignOut }: { onSignOut: () => void }) {
  const { pathname } = useLocation()
  const shell = useShell()
  const [custom, setCustom] = useState<Header | null>(null)
  const context = useMemo(() => ({ setHeader: setCustom }), [])
  const section = sectionOf(pathname)
  const header = custom ?? defaultHeader(pathname)

  return (
    <ShellContext.Provider value={context}>
      <ToastProvider>
        <div className="flex h-dvh flex-col overflow-hidden bg-background text-foreground">
          {!shell.online && <OfflineBanner />}
          <div className="flex min-h-0 flex-1">
            <Sidebar section={section} pathname={pathname} shell={shell} onSignOut={onSignOut} />
            <main className="flex min-w-0 flex-1 flex-col">
              <MobileBar header={header} online={shell.online} />
              <div className="min-h-0 flex-1 overflow-auto">
                <Outlet />
              </div>
              <TabBar section={section} pending={shell.pending} />
            </main>
          </div>
        </div>
      </ToastProvider>
    </ShellContext.Provider>
  )
}
```

Create `web/src/components/LegacyPage.tsx`:

```tsx
import { Outlet } from 'react-router'

/** The container of a page that is not rebuilt as a conversation yet. The part that rebuilds a page moves its route out of this wrapper. */
export default function LegacyPage() {
  return (
    <div className="mx-auto w-full max-w-5xl p-4 md:p-8">
      <Outlet />
    </div>
  )
}
```

In `web/src/App.tsx` import `LegacyPage from './components/LegacyPage.tsx'` and wrap every route inside the layout route in a pathless `LegacyPage` route, so that `<Routes>` reads:

```tsx
    <Routes>
      <Route element={<AppLayout onSignOut={signOut} />}>
        <Route element={<LegacyPage />}>
          <Route index element={<TimelinePage />} />
          <Route path="incidents" element={<IncidentsPage />} />
          <Route path="incidents/:id" element={<IncidentRoute />} />
          <Route path="approvals" element={<ApprovalsPage />} />
          <Route path="runs" element={<RunsPage />} />
          <Route path="runs/:id" element={<RunRoute />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="*" element={<NotFound />} />
        </Route>
      </Route>
    </Routes>
```

- [ ] **Step 6: Verify**

Run: `cd web && npm run lint && npm run build && npm test`
Expected: all pass, no warnings from `react/only-export-components` (the context and hooks live in `.ts` files for that reason).
Run: `grep -rn "usePendingApprovals" web/src`: no output.

- [ ] **Step 7: Commit**

```sh
git add web/src
git commit -m "feat(web): the conversation shell with sidebar, tab bar, offline banner and toast" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: The login

**Files:**
- Modify: `web/src/Login.tsx` (rewrite)

**Interfaces:**
- Consumes: `greeting(hour)` (Task 1), `RemedyMark` (Task 3), `api.login`, `ApiError`, `Button`, `Input`.
- Produces: the new `Login({ onLoggedIn })`, same props as before.

- [ ] **Step 1: Rewrite the login**

```tsx
import { useState } from 'react'
import type { FormEvent } from 'react'
import { api, ApiError } from './api.ts'
import { greeting } from './greeting.ts'
import RemedyMark from '@/components/RemedyMark'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

export default function Login({ onLoggedIn }: { onLoggedIn: () => void }) {
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api.login(password)
      onLoggedIn()
    } catch (err) {
      if (err instanceof ApiError) setError(err.status === 401 ? "That isn't the admin password." : err.message)
      else setError('Login failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="flex min-h-dvh items-center justify-center p-7">
      <div className="flex w-full max-w-100 flex-col gap-7">
        <span className="flex items-center gap-3">
          <RemedyMark size={40} />
          <span className="font-serif text-3xl font-medium tracking-tight">remedy</span>
        </span>
        <div className="flex flex-col gap-2.5">
          <h1 className="font-serif text-4xl leading-tight font-normal tracking-tight">{greeting(new Date().getHours())}.</h1>
          <p className="text-muted-foreground">Sign in with the admin password and I'll walk you through what happened.</p>
        </div>
        <form onSubmit={submit} className="flex flex-col gap-2.5">
          <Input
            type="password"
            autoFocus
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="Admin password"
            aria-label="Admin password"
            aria-invalid={error !== ''}
            className="h-13 text-[15px]"
          />
          {error && (
            <span role="alert" className="pl-5 text-[13px] text-destructive">
              {error}
            </span>
          )}
          <Button type="submit" size="lg" className="h-13" disabled={busy || password === ''}>
            Sign in
          </Button>
        </form>
      </div>
    </main>
  )
}
```

- [ ] **Step 2: Verify**

Run: `cd web && npm run lint && npm run build && npm test`
Expected: all pass.

- [ ] **Step 3: Commit**

```sh
git add web/src/Login.tsx
git commit -m "feat(web): the conversation login" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Verification (run by the controller, in a real browser)

This task changes no code unless it finds a defect; a defect goes to a fix dispatch. Nothing from here is committed except fixes.

- [ ] **Step 1: Full checks.** From the worktree root run `make web-install` if `web/node_modules` is missing, then `make check`. Expected: PASS (fmt, vet, test, web lint, web test, web build). Then `go test -tags webui ./web/` after `make web-build`. Expected: PASS.

- [ ] **Step 2: Run the app.** Start the server with `REMEDY_ADMIN_PASSWORD`, `REMEDY_RUNNER_TOKEN` and `REMEDY_MASTER_KEY` set and a throwaway database in the scratchpad (see the README quick start and `make dev-server`), and `make dev-web` for the Vite dev server (proxies `/api`). Seed three incidents by hand (CLAUDE.md: nine fractional digits in timestamps, oldest first): a GitHub one with a very long title (80+ characters), a diagnosing one, an alertmanager one with no repository. Never use a real database.

- [ ] **Step 3: Check the login** at desktop width 1280x800 and at 390x844: the greeting follows the hour; a wrong password shows "That isn't the admin password." and no other text; the pill input and button; the tab key shows a focus ring in amber.

- [ ] **Step 4: Check the shell** at both widths: the five items with the right active item on `/`, `/incidents`, `/incidents/<id>`, `/approvals`, `/runs`, `/runs/<id>` and `/settings` (`aria-current="page"`); the sidebar lists the three incidents with dots in the right colours, the long title truncates, "awake" is shown; at 390 px the top bar shows the title and, on a thread and a run, a back arrow; the tab bar sits under the content and the content never hides under it; a legacy page with a table (`/incidents`, `/settings`) scrolls inside its container and does not widen the page; stop the server: within a few seconds the offline banner shows and "dozing" appears; start it again: both go away. Start a server variant without the gatekeeper if the dev server has it on, or confirm by reading the network panel that a `404` on `/api/approvals` leaves the shell online. With no incidents seeded in a second database the sidebar says "No open incidents" only after the first answer.

- [ ] **Step 5: Check the toast.** Temporarily add `const toast = useToast()` to `Sidebar.tsx` and call `toast.show('Ignored #27.', () => console.log('undo'))` from the Sign out button's `onClick` before `onSignOut()` (do not commit this): the toast appears bottom centre (above the tab bar on a phone), disappears after about 4 seconds, a second click replaces it, Undo logs once and closes it. Revert the edit and confirm with `git diff` that nothing is left.

- [ ] **Step 6: Check reduced motion and keyboard.** With `prefers-reduced-motion: reduce` emulated, no animation runs. Tab through the shell: every link and button shows a focus ring. Delete stray `*.png` files and `.playwright-mcp/` in the checkout that launched the browser.

- [ ] **Step 7: Record the result** in the PR description: what was checked at which width, and anything that could not be checked.

---

## Self-Review

**Spec coverage (spec 4):** 4.1 theme: tokens, fonts, keyframes, reduced motion, pill shapes (Task 2). 4.2 shell: `h-dvh` with a scrolling content area, 300 px sidebar from `md`, 56 px top bar, tab bar with 44 px targets and the safe-area inset, five items with badge and active rules, sidebar with logo, awake/dozing, open conversations (dot, title, age; no preview line yet), Coming later, Sign out; shell hook, offline banner, `useShellHeader`, `LegacyPage`, mark and avatar, toast (Tasks 1 and 3). 4.3 login: greeting by hour, neutral line, 401 sentence, API message otherwise (Tasks 1 and 4). Sign out on a phone moves to Setup in part 5, as the spec says. Spec 10: `node --test` for pure logic, `make check`, browser checks (Tasks 1 and 5).

**Placeholder scan:** none. Task 2 steps describe edits of existing shadcn files as exact string replacements because those files are generated code of 20 to 115 lines; the new class strings are given in full.

**Type consistency:** `nextShell(prev, approvals, incidents, isHttpError)` (Task 1) is called with `Promise.allSettled` results in `useShell` (Task 3): `PromiseSettledResult<readonly unknown[]>` accepts `ToolCall[]` and `PromiseSettledResult<Incident[]>` accepts the incident list. `ShellState` has `pending`, `incidents`, `online`, `loaded` everywhere (`Sidebar`, `AppLayout`, tests). `Section`, `navItems`, `sectionOf`, `defaultHeader`, `Header` are imported with the same names in `AppLayout`, `Sidebar`, `TabBar`, `MobileBar`, `shellContext`. `RemedyMark` props `size`, `cutout`, `className`; used with `size` and `cutout`.

**Review Focus coverage:** 1, 3 and 2 (`nextShell` tests: HTTP error stays online, unreachable keeps data, `loaded`), 7 (`greeting` tests, Login code), 4, 5 and 6 (Task 3 code and the Task 5 browser steps).
