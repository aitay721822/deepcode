import { useEffect, useState } from 'react'
import { stopJob, syncRepo } from '../../api/repo/repos'
import { isActive } from '../../components/repo/jobStage'
import type { Job } from '../../types/repo/repo'

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

/**
 * Stops job on request: a queued one at once, a running one once its current step gives way, so asked holds until
 * the job has ended; error says why it could not be stopped (403, or 409 when it had ended anyway). onDone runs
 * either way. Sync runs a stopped job again, from the work it stored.
 */
export function useStop(job: Job | null | undefined, onDone?: () => void) {
  const [asked, setAsked] = useState(false)
  const [error, setError] = useState('')
  const active = isActive(job)
  useEffect(() => {
    if (active) return
    setAsked(false) // a Sync queues the same job again
    setError('')
  }, [active])
  async function run() {
    if (!job) return
    setAsked(true)
    setError('')
    try {
      await stopJob(job.id)
    } catch (e) {
      setAsked(false)
      setError((e as Error).message)
    } finally {
      onDone?.()
    }
  }
  return { active, asked, error, run }
}
