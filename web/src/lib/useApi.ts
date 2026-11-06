import { useEffect, useRef, useState } from 'react'

type State<T> = { key?: string; data?: T; error?: string; status?: number }

/**
 * Calls load for key (null = nothing yet), again every pollMs while set (less often while loads fail).
 * The key names what load fetches: a new key loads anew.
 * status is the HTTP status of the last failure, when the error carries one.
 */
export function useApi<T>(key: string | null, load: () => Promise<T>, pollMs?: number) {
  const [state, setState] = useState<State<T>>({})
  const [tick, setTick] = useState(0)
  const pending = useRef(false)
  // Polls to let pass after failures in a row: 1, 3, 7, then 15, so a server that is down is asked every 2, 4, 8, then
  // 16 times pollMs. Set as each load settles, so the first success puts polling back at pollMs.
  const failures = useRef(0)
  const skip = useRef(0)
  useEffect(() => {
    if (!key) return
    let live = true
    pending.current = true
    load()
      .then(
        (data) => {
          failures.current = skip.current = 0
          if (live) setState({ key, data })
        },
        // A failed poll keeps the last data for this key; never another key's.
        (e: Error & { status?: number }) => {
          skip.current = 2 ** Math.min(++failures.current, 4) - 1
          if (live) setState((s) => ({ key, data: s.key === key ? s.data : undefined, error: e.message, status: e.status }))
        },
      )
      .finally(() => live && (pending.current = false))
    return () => {
      live = false
    }
  }, [key, tick])
  useEffect(() => {
    if (!key || !pollMs) return
    failures.current = skip.current = 0 // another key: a count of its own
    // Skip a tick while the last load is out: a slow server must not see every request cancelled.
    const id = setInterval(() => {
      if (pending.current) return
      if (skip.current > 0) skip.current--
      else setTick((t) => t + 1)
    }, pollMs)
    return () => clearInterval(id)
  }, [key, pollMs])
  const current = state.key === key
  return {
    data: current ? state.data : undefined,
    error: current ? state.error : undefined,
    status: current ? state.status : undefined,
    reload: () => {
      setState((s) => ({ ...s, error: undefined, status: undefined })) // a retry starts without the last error
      setTick((t) => t + 1)
    },
  }
}
