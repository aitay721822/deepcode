import type { Job, Repo } from '../../types/repo/repo'

export const STAGES: Record<Job['kind'], [string, string][]> = {
  index: [
    ['fetch', 'Fetch the commit'],
    ['parse', 'Parse files and symbols'],
    ['embed', 'Embed chunks'],
    ['publish', 'Publish the snapshot'],
  ],
  wiki: [
    ['plan', 'Plan the wiki'],
    ['write', 'Write pages'],
  ],
}

export const stageIndex = (job: Job) =>
  Math.max(
    0,
    STAGES[job.kind].findIndex(([s]) => s === job.stage),
  )

/** How far the job is, 0 to 1: the stages behind it plus the current one's progress. */
export const jobFraction = (job: Job) =>
  job.status === 'succeeded'
    ? 1
    : (stageIndex(job) + (job.total ? job.progress / job.total : 0)) / STAGES[job.kind].length

/** The stage a job is at: "Parse files and symbols", or "Wiki queued" before it starts. */
export function stageName(job: Job) {
  if (job.status === 'queued') return job.kind === 'wiki' ? 'Wiki queued' : 'Indexing queued'
  return STAGES[job.kind][stageIndex(job)][1]
}

/** How far into its stage a job is, "120 / 900"; empty while the stage is not counted. */
export const stageCount = (job: Job) =>
  job.status !== 'queued' && job.total > 0 ? `${job.progress} / ${job.total}` : ''

/** One line for small spaces: "Parse files and symbols 120 / 900". */
export const stageLabel = (job: Job) => `${stageName(job)} ${stageCount(job)}`.trim()

export const isActive = (job: Job | null | undefined) => job?.status === 'queued' || job?.status === 'running'

/** Indexed, with no wiki yet: the wiki job is queued just after the snapshot is published, so look again shortly. */
export const awaitingWiki = (repo: Repo) =>
  repo.job?.kind === 'index' && repo.job.status === 'succeeded' && repo.snapshot?.wikiStatus === 'none'
