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

/**
 * Puts the browser's own words for a request that failed on the way ("Failed to fetch", "Load failed", "network
 * error", "signal timed out", …) as a sentence the reader can act on. An abort the caller asked for goes on as it is.
 */
export function unreachable(e: unknown): never {
  if (e instanceof TypeError) throw new Error('Cannot reach the server. Check the connection and try again.')
  if (e instanceof DOMException && e.name === 'TimeoutError')
    throw new Error('The server took too long to answer. Try again.')
  throw e
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  // A read (no init) that hangs, on a half-open connection or after the computer slept, gives up after 30s, so a poll
  // behind it goes on. Writes may take longer, such as an import's first look at the remote.
  const res = await fetch(path, init ?? { signal: AbortSignal.timeout(30_000) }).catch(unreachable)
  if (!res.ok) throw await httpError(res)
  if (res.status === 204) return {} as T // nothing to read
  return res.json().catch(unreachable)
}

const json = { 'Content-Type': 'application/json' }

export const post = <T>(path: string, data: unknown) =>
  api<T>(path, { method: 'POST', headers: json, body: JSON.stringify(data) })

export const patch = <T>(path: string, data: unknown) =>
  api<T>(path, { method: 'PATCH', headers: json, body: JSON.stringify(data) })

export const del = (path: string) => api<unknown>(path, { method: 'DELETE', headers: json })

export type StreamState = 'open' | 'retrying' | 'closed'

/**
 * Opens a server-sent event stream and hands each named event's JSON to its handler.
 * EventSource reconnects by itself ('retrying', then 'open'); 'closed' means it gave up,
 * e.g. on a 401 it cannot report. Returns the function that closes the stream.
 * The typed wrappers in api/* narrow each handler's data: a handler of never takes any of them.
 */
export function listen(path: string, on: Record<string, (data: never) => void>, state?: (s: StreamState) => void) {
  const es = new EventSource(path)
  for (const [name, handle] of Object.entries(on))
    es.addEventListener(name, (e) => handle(JSON.parse((e as MessageEvent).data) as never))
  es.onopen = () => state?.('open')
  es.onerror = () => state?.(es.readyState === EventSource.CLOSED ? 'closed' : 'retrying')
  return () => es.close()
}
