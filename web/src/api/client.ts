let unauthorized = () => {}

/** Runs on every 401 (no session, or it expired); the session layer decides where to go. */
export function setUnauthorizedHandler(fn: () => void) {
  unauthorized = fn
}

/** The error a failed response carries ({ "error": "…" }), else its status text; status is kept on it. */
export async function httpError(res: Response) {
  if (res.status === 401) unauthorized()
  const body = await res.json().catch(() => ({}))
  return Object.assign(new Error(body.error || res.statusText || `HTTP ${res.status}`), { status: res.status })
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, init)
  if (!res.ok) throw await httpError(res)
  return res.json().catch(() => ({})) // 204: nothing to read
}

const json = { 'Content-Type': 'application/json' }

export const post = <T>(path: string, data: unknown) => api<T>(path, { method: 'POST', headers: json, body: JSON.stringify(data) })

export const patch = <T>(path: string, data: unknown) => api<T>(path, { method: 'PATCH', headers: json, body: JSON.stringify(data) })

export const del = (path: string) => api<unknown>(path, { method: 'DELETE', headers: json })

export type StreamState = 'open' | 'retrying' | 'closed'

/**
 * Opens a server-sent event stream and hands each named event's JSON to its handler.
 * EventSource reconnects by itself ('retrying', then 'open'); 'closed' means it gave up,
 * e.g. on a 401 it cannot report. Returns the function that closes the stream.
 * The typed wrappers in api/* narrow each handler's data.
 */
export function listen(path: string, on: Record<string, (data: any) => void>, state?: (s: StreamState) => void) {
  const es = new EventSource(path)
  for (const [name, handle] of Object.entries(on)) es.addEventListener(name, (e) => handle(JSON.parse((e as MessageEvent).data)))
  es.onopen = () => state?.('open')
  es.onerror = () => state?.(es.readyState === EventSource.CLOSED ? 'closed' : 'retrying')
  return () => es.close()
}
