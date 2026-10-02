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
}

export interface RunEvent {
  seq: number
  kind: string
  payload: unknown
  createdAt: string
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
