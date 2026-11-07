import { useEffect, useState } from 'react'
import { syncRepo } from '../../api/repo/repos'

export type Sync = ReturnType<typeof useSync>

/** Syncs a repository on request: busy meanwhile, then why it failed, or that it was already up to date; onDone runs either way. */
export function useSync(repoId: string, onDone: () => void) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [upToDate, setUpToDate] = useState(false)
  useEffect(() => {
    if (!upToDate) return
    const id = setTimeout(() => setUpToDate(false), 5000) // true when asked, not for long: a push can come any time
    return () => clearTimeout(id)
  }, [upToDate])
  async function run() {
    setBusy(true)
    setError('')
    setUpToDate(false)
    try {
      setUpToDate(!!(await syncRepo(repoId)).upToDate)
    } catch (e) {
      setError((e as Error).message) // e.g. 403, or 502 when git cannot reach the server
    } finally {
      onDone()
      setBusy(false)
    }
  }
  return { busy, error, upToDate, run }
}
