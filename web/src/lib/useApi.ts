import { useCallback, useEffect, useEffectEvent, useRef, useState } from 'react'

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
  // The latest load, without re-running the effect: callers pass a new closure every render, and the key alone says
  // what it fetches.
  const loadLatest = useEffectEvent(load)
  useEffect(() => {
    if (!key) return
    let live = true
    pending.current = true
    loadLatest()
      .then(
        (data) => {
          failures.current = skip.current = 0
          if (live) setState({ key, data })
        },
        // A failed poll keeps the last data for this key; never another key's.
        (e: Error & { status?: number }) => {
          skip.current = 2 ** Math.min(++failures.current, 4) - 1
          if (live)
            setState((s) => ({ key, data: s.key === key ? s.data : undefined, error: e.message, status: e.status }))
        },
      )
      .finally(() => {
        if (live) pending.current = false
      })
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
  // Stable, so an effect can list it without re-running.
  const reload = useCallback(() => {
    setState((s) => ({ ...s, error: undefined, status: undefined })) // a retry starts without the last error
    setTick((t) => t + 1)
  }, [])
  const current = state.key === key
  return {
    data: current ? state.data : undefined,
    error: current ? state.error : undefined,
    status: current ? state.status : undefined,
    reload,
  }
}
