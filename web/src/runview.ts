import type { Run, RunEvent, RunStatus } from './api.ts'

/** One thing the agent did: what it did in words, the raw line, and whether it has an answer yet. */
export interface Step {
  id: string
  label: string
  raw: string
  /** The result has arrived. */
  done: boolean
  /** The result is an error. */
  failed: boolean
}

const PREFIX = 'mcp__remedy__'

/** The name of a tool without the prefix of the gatekeeper's server. */
export function shortTool(name: string): string {
  return name.startsWith(PREFIX) ? name.slice(PREFIX.length) : name
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/** One tidy line of text: whitespace collapsed, cut at `max` characters. */
function line(text: string, max: number): string {
  const flat = text.replace(/\s+/g, ' ').trim()
  return flat.length > max ? `${flat.slice(0, max)}…` : flat
}

const MAX_VALUE = 80

/** A text or number value of an input as one tidy line, cut at 80 characters, or undefined when it is missing, empty or of another type. */
function value(input: unknown, name: string): string | undefined {
  if (!isRecord(input)) return undefined
  const v = input[name]
  if (typeof v === 'string') return line(v, MAX_VALUE) || undefined
  if (typeof v === 'number') return String(v)
  return undefined
}

/**
 * What a tool call means, in Remedy's words, from a fixed table. The sentence is ours; the values in it come from the agent's
 * arguments and are only inserted. A missing value falls back to the general phrase.
 */
export function stepLabel(tool: string, input: unknown): string {
  const name = shortTool(tool)
  const v = (n: string) => value(input, n)
  switch (name) {
    case 'incident_get':
      return v('id') ? `Read incident #${v('id')}` : 'Read an incident'
    case 'incident_list':
      return 'Looked at the incidents'
    case 'incident_job_log':
      return v('id') ? `Read the failure log of incident #${v('id')}` : 'Read a failure log'
    case 'activity_list':
      return 'Read the activity log'
    case 'incident_add_note':
      return v('id') ? `Asked to add a note to incident #${v('id')}` : 'Asked to add a note'
    case 'cluster_workloads':
      return v('namespace') ? `Looked at the workloads in ${v('namespace')}` : 'Looked at the workloads'
    case 'cluster_pods':
      return v('namespace') ? `Looked at the pods in ${v('namespace')}` : 'Looked at the pods'
    case 'cluster_describe':
      return v('kind') && v('name') && v('namespace')
        ? `Looked at ${v('kind')} ${v('name')} in ${v('namespace')}`
        : v('kind') && v('name')
          ? `Looked at ${v('kind')} ${v('name')}`
          : 'Looked at a resource'
    case 'cluster_events':
      return v('namespace') ? `Read the events in ${v('namespace')}` : 'Read the events'
    case 'cluster_pod_logs':
      return v('pod') ? `Read the logs of ${v('pod')}` : 'Read the logs of a pod'
    case 'cluster_nodes':
      return 'Looked at the nodes'
    case 'argo_apps':
      return v('name') ? `Looked at the Argo CD application ${v('name')}` : 'Looked at the Argo CD applications'
    case 'cluster_rollout_restart':
      return v('name') && v('namespace') ? `Asked to restart ${v('name')} in ${v('namespace')}` : 'Asked to restart a workload'
    case 'cluster_delete_pod':
      return v('name') && v('namespace') ? `Asked to delete the pod ${v('name')} in ${v('namespace')}` : 'Asked to delete a pod'
    case 'argo_refresh':
      return v('app') ? `Asked to refresh the application ${v('app')}` : 'Asked to refresh an application'
    case 'argo_sync':
      return v('app') ? `Asked to sync the application ${v('app')}` : 'Asked to sync an application'
    case 'Read':
      return v('file_path') ? `Read ${v('file_path')}` : 'Read a file'
    case 'Grep':
      return v('pattern') ? `Searched for "${v('pattern')}"` : 'Searched the files'
    case 'Glob':
      return v('pattern') ? `Listed the files matching ${v('pattern')}` : 'Listed files'
  }
  return `Used ${line(name, 80)}`
}

const MAX_ARGS = 240
const MAX_RESULT = 160

function resultText(content: unknown): string {
  if (typeof content === 'string') return content
  if (Array.isArray(content)) {
    return content
      .map((b) => (isRecord(b) && typeof b.text === 'string' ? b.text : ''))
      .filter((t) => t !== '')
      .join(' ')
  }
  return ''
}

function blocks(payload: unknown): unknown[] {
  if (!isRecord(payload) || !isRecord(payload.message)) return []
  const content = payload.message.content
  return Array.isArray(content) ? content : []
}

/**
 * The steps of a run, from its events: each `tool_use` of an assistant event is paired with the `tool_result` of a later user event
 * by the id of the call. The order is the order of the calls. The synthetic structured-output call is not a step. Everything the agent
 * wrote is text: it is cut and shown, never interpreted. Events that are not what is expected are skipped.
 */
export function runSteps(events: readonly RunEvent[]): Step[] {
  const steps: Step[] = []
  const byId = new Map<string, Step>()
  for (const e of events) {
    if (e.kind === 'assistant') {
      for (const b of blocks(e.payload)) {
        if (!isRecord(b) || b.type !== 'tool_use' || typeof b.name !== 'string') continue
        if (b.name === 'StructuredOutput') continue
        const id = typeof b.id === 'string' ? b.id : `seq${e.seq}-${steps.length}`
        const args = b.input === undefined ? '' : (JSON.stringify(b.input) ?? '')
        const step: Step = {
          id,
          label: stepLabel(b.name, b.input),
          raw: `${shortTool(b.name)} ${line(args, MAX_ARGS)}`.trim(),
          done: false,
          failed: false,
        }
        steps.push(step)
        byId.set(id, step)
      }
    } else if (e.kind === 'user') {
      for (const b of blocks(e.payload)) {
        if (!isRecord(b) || b.type !== 'tool_result' || typeof b.tool_use_id !== 'string') continue
        const step = byId.get(b.tool_use_id)
        if (!step) continue
        const failed = b.is_error === true
        step.done = true
        step.failed = failed
        const text = line(resultText(b.content), MAX_RESULT)
        if (failed) step.raw = `${step.raw} → error${text ? `: ${text}` : ''}`
        else if (text) step.raw = `${step.raw} → ${text}`
      }
    }
  }
  return steps
}

/** A queued run that already has events is in fact running: events exist only after the runner has started it. */
export function effectiveStatus(status: RunStatus, eventCount: number): RunStatus {
  return status === 'queued' && eventCount > 0 ? 'running' : status
}

/** Whether "Cancel run" is offered: a queued run, or a running run that has the gatekeeper (the runner can then be told). */
export function canCancel(run: Run, status: RunStatus): boolean {
  const ended = status === 'succeeded' || status === 'failed'
  return !ended && !run.cancelRequested && (status === 'queued' || (status === 'running' && run.mcp === true))
}

/** What went wrong with a failed run, in Remedy's words. The run's own text, when there is one, is shown under the title. */
export function runFailure(run: Run): { title: string; text: string } | undefined {
  if (run.status !== 'failed') return undefined
  const own = run.result?.trim() ?? ''
  switch (run.failureReason) {
    case 'runner_lost':
      return { title: 'The runner was lost', text: own || 'The runner stopped sending heartbeats, so the run was failed. Nothing was changed.' }
    case 'timeout':
      return { title: 'It took too long', text: own || 'The run stayed running longer than its time budget, so it was stopped.' }
    case 'invalid_output':
      return { title: "The answer wasn't valid", text: own || 'The agent finished, but its answer did not match what I asked for.' }
    case 'cancelled':
      return { title: 'Cancelled', text: own || 'The run was cancelled. The runner stopped the agent.' }
  }
  return { title: 'The run failed', text: own || 'The agent stopped with an error.' }
}

/** The line under the status of a run: its kind, its tools and its time. */
export function runMeta(run: Run, time: string): string {
  return [run.role === 'responder' ? 'Responder' : 'Ad-hoc', run.mcp && 'tools', run.cluster && 'cluster', time].filter(Boolean).join(' · ')
}

/** One line of text for the raw output: the text of a result, a plain string, or the start of the JSON. */
export function summarizeEvent(e: RunEvent): string {
  if (e.kind === 'result' && isRecord(e.payload) && typeof e.payload.result === 'string') return e.payload.result
  if (typeof e.payload === 'string') return e.payload
  const text = JSON.stringify(e.payload) ?? ''
  return text.length > 600 ? `${text.slice(0, 600)}...` : text
}
