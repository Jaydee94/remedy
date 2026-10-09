import type { RunnerStatus } from './api.ts'
import { duration } from './setup.ts'

/** A chip, in the colour tokens of the app. */
export interface Chip {
  label: string
  dot: string
  soft: string
  text: string
}

/** What the Setup page says about the runner. Every sentence is a template; nothing the runner sends decides its structure. */
export interface RunnerView {
  connection: Chip
  login: Chip
  sentence: string
  /** What to do, when there is something to do. */
  hint: string | null
  /** A fact about the past, outside the live sentence: it changes with the clock, the sentence must not. */
  detail: string | null
  /** A command to copy, shown apart from the hint. */
  command: string | null
  /** The CLI's version line. Only a runner that is connected has one: a gone runner's version is a fact about the past. */
  version: string | null
}

const good = { dot: 'bg-success', soft: 'bg-soft-resolved', text: 'text-success' }
const bad = { dot: 'bg-destructive', soft: 'bg-soft-open', text: 'text-destructive' }
const neutral = { dot: 'bg-muted-foreground', soft: 'bg-muted', text: 'text-muted-foreground' }

/** How to log the runner in once; the command follows it. */
export const loginHint = 'Log in once in the runner pod, then type /login:'

/** The runner pod's name is fixed by the chart. */
export const loginCommand = 'kubectl exec -it remedy-runner-0 -c runner -- /opt/claude/claude'

const checkPod = 'Check that the runner is running: in Kubernetes the pod is remedy-runner-0.'

/** A control plane without `GET /api/runner` (an older one) answers 404: the section then says nothing instead of an error. */
export function hideOnError(status: number): boolean {
  return status === 404
}

/** The runner's state as the page shows it. A runner that is not connected has an unknown login, whatever the status says. */
export function runnerView(s: RunnerStatus, now: number): RunnerView {
  const connection: Chip = s.connected ? { label: 'Connected', ...good } : s.lastSeenAt ? { label: 'Not connected', ...bad } : { label: 'Never seen', ...neutral }
  const login: Chip =
    !s.connected || s.login === 'unknown'
      ? { label: 'Login unknown', ...neutral }
      : s.login === 'ok'
        ? { label: 'Logged in', ...good }
        : { label: 'Not logged in', ...bad }
  const version = s.connected && s.cliVersion ? s.cliVersion : null

  if (!s.connected) {
    if (!s.lastSeenAt) {
      return { connection, login, sentence: 'I have not heard from my runner yet.', hint: checkPod, detail: null, command: null, version }
    }
    const silent = Math.max(0, Math.round((now - Date.parse(s.lastSeenAt)) / 1000))
    return { connection, login, sentence: 'My runner is not connected.', hint: checkPod, detail: `I last heard from it ${duration(silent)} ago.`, command: null, version }
  }
  switch (s.login) {
    case 'ok':
      return { connection, login, sentence: 'My runner is connected and logged in, so I can run an agent.', hint: null, detail: null, command: null, version }
    case 'missing':
      return { connection, login, sentence: 'My runner is connected, but the agent is not logged in, so I cannot run one.', hint: loginHint, detail: null, command: loginCommand, version }
    default:
      return { connection, login, sentence: 'My runner is connected. I do not know yet whether the agent is logged in.', hint: null, detail: null, command: null, version }
  }
}
