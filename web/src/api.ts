export type RunStatus = 'queued' | 'running' | 'succeeded' | 'failed'

export interface Run {
  id: string
  provider: string
  prompt: string
  status: RunStatus
  exitCode?: number
  result: string
  sessionId: string
  costUsd: number
  createdAt: string
  startedAt?: string
  finishedAt?: string
  role: 'adhoc' | 'responder'
  incidentId?: number
  /** Why a failed run failed, when it was not the agent's own exit code. */
  failureReason?: 'timeout' | 'invalid_output' | 'cancelled' | 'runner_lost'
  /** The run has access to the gatekeeper's tools. */
  mcp?: boolean
  /** The run has the cluster tools of the gatekeeper (it was started with the chip "Read the cluster"). */
  cluster?: boolean
  /** The maintainer cancelled the run; the runner is stopping the agent. */
  cancelRequested?: boolean
  /** The id of the approval the run waits for. Only `GET /api/runs/{id}` has it, and only while there is one. */
  waitingApproval?: number
}

/** What the control plane can do in a cluster. It carries no address and no token. */
export interface Capabilities {
  cluster: {
    /** The cluster can be read: a run can be started with cluster tools. */
    read: boolean
    /** Actions in the cluster are possible, each one after an approval. */
    write: boolean
    /** The namespaces in which actions may be used. */
    namespaces: string[]
  }
}

/** A call of an agent to a gatekeeper tool: a row of the audit log, and for a mutating tool an approval. */
export interface ToolCall {
  id: number
  runId: string
  incidentId?: number
  tool: string
  kind: 'read' | 'mutating'
  /** What the agent asked for. Usually an object. Show it as text, never as HTML. */
  arguments: unknown
  status: 'running' | 'waiting' | 'succeeded' | 'failed' | 'denied' | 'abandoned'
  /** Empty for a read tool. */
  decision: '' | 'pending' | 'approved' | 'denied' | 'abandoned'
  reason?: string
  result?: string
  error?: string
  requestedAt: string
  decidedAt?: string
  finishedAt?: string
  /** An agent still waits for the answer of the call: only then can it be decided. */
  waiting: boolean
}

export interface RunEvent {
  seq: number
  kind: string
  payload: unknown
  createdAt: string
}

export interface GitHubConnection {
  connected: boolean
  login?: string
  /** Only the last four characters of the token, never the token itself. */
  tokenHint?: string
  status?: 'ok' | 'error' | 'undecryptable'
  statusDetail?: string
  checkedAt?: string
}

export interface Repo {
  id: number
  fullName: string
  defaultBranch: string
  enabled: boolean
  lastPolledAt?: string
  lastError: string
  createdAt: string
}

export type IncidentState = 'open' | 'diagnosing' | 'diagnosed' | 'resolved' | 'ignored'

export type IncidentSource = 'github' | 'alertmanager' | 'argocd'

export interface Incident {
  id: number
  source: IncidentSource
  /** The line of the list. For a GitHub incident it is the check name. */
  title: string
  severity: 'critical' | 'warning' | 'info' | 'none'
  /** The responder may start on its own. */
  autoDiagnose: boolean
  /** The signal of an incident from a source other than GitHub: labels, annotations, the state of an application. */
  details?: Record<string, unknown>
  /** The fields from here to checkUrl belong to GitHub. They are empty for another source. */
  repoId: number
  repo: string
  /** "pr:<number>" or "branch:<name>". */
  ref: string
  refUrl?: string
  checkName: string
  state: IncidentState
  conclusion: string
  headSha: string
  checkUrl?: string
  occurrences: number
  firstSeen: string
  lastSeen: string
  resolvedAt?: string
  resolvedReason?: string
  /** Automatic diagnoses started for this incident. */
  diagnoses: number
  lastDiagnosisAt?: string
  diagnosis?: Diagnosis
  /** The commit the diagnosis is about. It differs from headSha when a newer commit failed since. */
  diagnosedSha?: string
  /** The latest responder run. */
  runId?: string
}

export interface ActivityEntry {
  id: number
  at: string
  kind: string
  summary: string
}

export interface IncidentDetail {
  incident: Incident
  activity: ActivityEntry[]
}

/** The answer of the responder agent. The names are those of the schema. Show it as text, never as HTML. */
export interface Diagnosis {
  summary: string
  cause: string
  confidence: 'high' | 'medium' | 'low'
  category: string
  affected_files: string[]
  proposed_fix: string
  fix_looks_automatable: boolean
}

export interface Limits {
  pollIntervalSeconds: number
  diagnoseCooldownSeconds: number
  diagnoseMaxPerIncident: number
  /** 0 means automatic diagnosis is off. */
  diagnoseMaxPerDay: number
  staleRunMinutes: number
}

/** One line of the timeline. The summary may contain text from GitHub: show it as text, never as HTML. */
export interface TimelineEntry {
  id: number
  at: string
  kind: string
  summary: string
  repo?: string
  incidentId?: number
  runId?: string
}

export interface TimelinePage {
  /** Newest first. */
  entries: TimelineEntry[]
  hasMore: boolean
}

export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: { 'Content-Type': 'application/json', 'X-Remedy-CSRF': '1' },
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
  })
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string }
    throw new ApiError(res.status, data.error ?? res.statusText)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export const api = {
  login: (password: string) => request<void>('POST', '/api/login', { password }),
  logout: () => request<void>('POST', '/api/logout'),
  me: () => request<{ user: string }>('GET', '/api/me'),
  listRuns: () => request<Run[]>('GET', '/api/runs'),
  createRun: (prompt: string, tools = false, cluster = false, incidentId?: number) =>
    request<Run>('POST', '/api/runs', incidentId === undefined ? { prompt, tools, cluster } : { prompt, tools, cluster, incidentId }),
  getCapabilities: () => request<Capabilities>('GET', '/api/capabilities'),
  getRun: (id: string) => request<Run>('GET', `/api/runs/${id}`),
  listToolCalls: (runId: string) => request<ToolCall[]>('GET', `/api/runs/${runId}/tool-calls`),
  cancelRun: (id: string) => request<void>('POST', `/api/runs/${id}/cancel`),

  /** The mutating calls of agents: "pending" are the ones that wait for a decision, "all" is the history as well. */
  listApprovals: (status: 'pending' | 'all') => request<ToolCall[]>('GET', `/api/approvals?status=${status}`),
  decideApproval: (id: number, approve: boolean, reason?: string) =>
    request<ToolCall>('POST', `/api/approvals/${id}/${approve ? 'approve' : 'deny'}`, reason ? { reason } : undefined),

  getConnection: () => request<GitHubConnection>('GET', '/api/github/connection'),
  putConnection: (token: string) => request<GitHubConnection>('PUT', '/api/github/connection', { token }),
  checkConnection: () => request<GitHubConnection>('POST', '/api/github/connection/check'),
  deleteConnection: () => request<void>('DELETE', '/api/github/connection'),
  listRepos: () => request<Repo[]>('GET', '/api/repos'),
  addRepo: (fullName: string) => request<Repo>('POST', '/api/repos', { fullName }),
  setRepoEnabled: (id: number, enabled: boolean) => request<void>('PATCH', `/api/repos/${id}`, { enabled }),
  deleteRepo: (id: number) => request<void>('DELETE', `/api/repos/${id}`),

  /** state is "active", "all" or one incident state. source is left out for every source. */
  listIncidents: (state: string, repoId?: number, source?: IncidentSource) => {
    const query = new URLSearchParams({ state })
    if (repoId !== undefined) query.set('repo', String(repoId))
    if (source !== undefined) query.set('source', source)
    return request<Incident[]>('GET', `/api/incidents?${query.toString()}`)
  },
  getIncident: (id: number) => request<IncidentDetail>('GET', `/api/incidents/${id}`),
  ignoreIncident: (id: number) => request<Incident>('POST', `/api/incidents/${id}/ignore`),
  unignoreIncident: (id: number) => request<Incident>('POST', `/api/incidents/${id}/unignore`),
  /** The runs of one incident, newest first: its responder runs and the questions asked about it. */
  listIncidentRuns: (incidentId: number) => request<Run[]>('GET', `/api/runs?incident=${incidentId}`),
  diagnoseIncident: (id: number) => request<{ runId: string }>('POST', `/api/incidents/${id}/diagnose`),
  getLimits: () => request<Limits>('GET', '/api/limits'),

  /** The newest entries, or the ones before the entry with the id `before`. */
  listActivity: (before?: number) =>
    request<TimelinePage>('GET', before === undefined ? '/api/activity' : `/api/activity?before=${before}`),
}

/** 'closed' means the browser gave up, for example because the session ended and the server answered 401. */
export type StreamState = 'live' | 'reconnecting' | 'closed'

/**
 * Follows the activity log from the entry with the id `after` on (0 means from the start). The browser
 * reconnects on its own and then sends Last-Event-ID, which the server prefers to `after`.
 */
export function streamActivity(
  after: number,
  onEntry: (e: TimelineEntry) => void,
  onState: (state: StreamState) => void,
): () => void {
  const es = new EventSource(`/api/activity/stream?after=${after}`)
  es.onopen = () => onState('live')
  es.onerror = () => onState(es.readyState === EventSource.CLOSED ? 'closed' : 'reconnecting')
  es.addEventListener('activity', (m) => onEntry(JSON.parse((m as MessageEvent<string>).data) as TimelineEntry))
  return () => es.close()
}

/** Opens the live stream of a run. The browser reconnects with Last-Event-ID on its own. */
export function streamRun(
  id: string,
  onEvent: (e: RunEvent) => void,
  onDone: (r: Run) => void,
): () => void {
  const es = new EventSource(`/api/runs/${id}/events`)
  es.addEventListener('run_event', (m) => onEvent(JSON.parse((m as MessageEvent<string>).data) as RunEvent))
  es.addEventListener('done', (m) => {
    onDone(JSON.parse((m as MessageEvent<string>).data) as Run)
    es.close()
  })
  return () => es.close()
}
