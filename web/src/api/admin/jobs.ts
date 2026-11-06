import type { JobDetail, JobRow, LogLine } from '../../types/admin/jobs'
import type { Job } from '../../types/repo/repo'
import { api, listen, type StreamState } from '../client'

/** The newest jobs first, optionally only those in one status. */
export const listJobs = (status: Job['status'] | '') => api<JobRow[]>(`/api/admin/jobs?limit=100${status ? `&status=${status}` : ''}`)

export const getJob = (id: number) => api<JobDetail>(`/api/admin/jobs/${id}`)

/** Every job change, as it happens. Returns the closer. */
export const watchJobs = (job: (j: JobRow) => void, state: (s: StreamState) => void) => listen('/api/admin/jobs/events', { job }, state)

/** The job's output lines after `after`, its changes, and `end` once it succeeded or failed. Returns the closer. */
export const watchJobOutput = (
  id: number,
  after: number,
  on: { log: (l: LogLine) => void; job: (j: JobRow) => void; end: (j: JobRow) => void },
  state: (s: StreamState) => void,
) => listen(`/api/admin/jobs/${id}/output?after=${after}`, on, state)
