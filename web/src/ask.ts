import type { ToolCall } from './api.ts'

/** What Remedy says when a call waits for a decision: the question, and the label of the Yes button. */
export interface Ask {
  question: string
  yes: string
}

/** A text or number argument of a call, or undefined when it is missing, empty or of another type. */
function arg(args: unknown, name: string): string | undefined {
  if (typeof args !== 'object' || args === null || Array.isArray(args)) return undefined
  const value = (args as Record<string, unknown>)[name]
  if (typeof value === 'string' && value !== '') return value
  if (typeof value === 'number') return String(value)
  return undefined
}

/**
 * The question and the Yes label of a call, from a fixed table. The sentence is ours; the values in it come from the agent's
 * arguments and are only inserted. A call with a value missing falls back to the general question instead of printing a hole.
 * The arguments are shown next to the question in full: what is shown is what runs.
 */
export function askText(call: Pick<ToolCall, 'tool' | 'arguments'>): Ask {
  const a = (name: string) => arg(call.arguments, name)
  switch (call.tool) {
    case 'cluster_rollout_restart': {
      const name = a('name')
      const namespace = a('namespace')
      if (name && namespace) return { question: `May I restart ${name} in ${namespace}?`, yes: 'Yes, restart it' }
      break
    }
    case 'cluster_delete_pod': {
      const name = a('name')
      const namespace = a('namespace')
      if (name && namespace) return { question: `May I delete the pod ${name} in ${namespace}?`, yes: 'Yes, delete it' }
      break
    }
    case 'argo_refresh': {
      const app = a('app')
      if (app) return { question: `May I refresh the application ${app}?`, yes: 'Yes, refresh it' }
      break
    }
    case 'argo_sync': {
      const app = a('app')
      if (app) return { question: `May I sync the application ${app}?`, yes: 'Yes, sync it' }
      break
    }
    case 'incident_add_note': {
      const id = a('id')
      if (id) return { question: `May I add a note to incident #${id}?`, yes: 'Yes, add it' }
      break
    }
  }
  return { question: `May I run ${call.tool}?`, yes: 'Yes, run it' }
}
