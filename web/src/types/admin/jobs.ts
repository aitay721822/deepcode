import type { Job } from '../repo/repo'

export type JobRow = Job & { repoOwner: string; repoName: string }

export type LogLine = { id: number; at: string; line: string }

export type JobDetail = { job: JobRow; logs: LogLine[] }
