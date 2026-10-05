import type { Capabilities, Run, ToolCall } from './api.ts'
import { runPhase } from './status.ts'
import type { RunPhase } from './status.ts'

export interface AskSwitches {
  tools: boolean
  cluster: boolean
}

/** The tools chip. The cluster tools are a part of the gatekeeper's tools: without the tools there is no cluster. */
export function toggleTools(s: AskSwitches): AskSwitches {
  const tools = !s.tools
  return { tools, cluster: tools ? s.cluster : false }
}

/** The cluster chip. The cluster tools need the gatekeeper: switching the cluster on switches the tools on. */
export function toggleCluster(s: AskSwitches): AskSwitches {
  const cluster = !s.cluster
  return { cluster, tools: cluster ? true : s.tools }
}

/** What the agent can do with the switches as they are, from real data. */
export function askHint(s: AskSwitches, caps: Capabilities | null): string {
  if (!s.tools) return 'I can only read the files of my workspace.'
  if (!s.cluster) return 'I can read incidents and ask to add a note to one. Every note waits for your decision under Needs you, and I wait with it.'
  if (caps?.cluster.write) {
    return `I can read the cluster. Actions in the cluster wait for your decision under Needs you; they are possible in: ${caps.cluster.namespaces.join(', ')}.`
  }
  return 'I can read the cluster. No action in the cluster is possible: no write access is configured.'
}

const TITLE_MAX = 140

/** One tidy line of text: whitespace collapsed, cut at `max` characters. */
function oneLine(text: string, max: number): string {
  const flat = text.replace(/\s+/g, ' ').trim()
  return flat.length > max ? `${flat.slice(0, max).trimEnd()}…` : flat
}

export interface RecentRun {
  id: string
  title: string
  phase: RunPhase
  at: string
  tools: boolean
  cluster: boolean
}

/**
 * A row of "Earlier conversations". An ad-hoc run is its question; a responder run is named by its incident and never shows its
 * prompt (it holds data from GitHub). A running run waits for you when a pending approval belongs to it: the run list does not say so.
 */
export function recentRun(run: Run, asks: readonly ToolCall[]): RecentRun {
  const waiting = run.status === 'running' && asks.some((c) => c.runId === run.id)
  const title =
    run.role === 'responder'
      ? run.incidentId !== undefined
        ? `Diagnosis of incident #${run.incidentId}`
        : 'Diagnosis of an incident'
      : oneLine(run.prompt, TITLE_MAX) || 'A run without a question'
  return { id: run.id, title, phase: runPhase(run.status, waiting), at: run.createdAt, tools: run.mcp === true, cluster: run.cluster === true }
}
