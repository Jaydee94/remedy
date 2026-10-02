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
  /** Set when the run was stopped for taking too long. */
  failureReason?: 'timeout'
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

export interface Incident {
  id: number
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
  createRun: (prompt: string) => request<Run>('POST', '/api/runs', { prompt }),
  getRun: (id: string) => request<Run>('GET', `/api/runs/${id}`),

  getConnection: () => request<GitHubConnection>('GET', '/api/github/connection'),
  putConnection: (token: string) => request<GitHubConnection>('PUT', '/api/github/connection', { token }),
  checkConnection: () => request<GitHubConnection>('POST', '/api/github/connection/check'),
  deleteConnection: () => request<void>('DELETE', '/api/github/connection'),
  listRepos: () => request<Repo[]>('GET', '/api/repos'),
  addRepo: (fullName: string) => request<Repo>('POST', '/api/repos', { fullName }),
  setRepoEnabled: (id: number, enabled: boolean) => request<void>('PATCH', `/api/repos/${id}`, { enabled }),
  deleteRepo: (id: number) => request<void>('DELETE', `/api/repos/${id}`),

  /** state is "active", "all" or one incident state. */
  listIncidents: (state: string, repoId?: number) => {
    const query = new URLSearchParams({ state })
    if (repoId !== undefined) query.set('repo', String(repoId))
    return request<Incident[]>('GET', `/api/incidents?${query.toString()}`)
  },
  getIncident: (id: number) => request<IncidentDetail>('GET', `/api/incidents/${id}`),
  ignoreIncident: (id: number) => request<Incident>('POST', `/api/incidents/${id}/ignore`),
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
