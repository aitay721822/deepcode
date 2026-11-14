import { formatTime, isPast } from '../../lib/format'
import type { Job, Repo } from '../../types/repo/repo'

export const STAGES: Record<Job['kind'], [string, string][]> = {
  index: [
    ['fetch', '取得 Commit'],
    ['parse', '解析檔案與 Symbol'],
    ['embed', '建立 Chunk 向量'],
    ['publish', '發布版本'],
  ],
  wiki: [
    ['plan', '規劃 Wiki'],
    ['write', '撰寫頁面'],
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

/**
 * A queued job that ran before and runs again: after an attempt that failed, its error saying why (the queue
 * re-queues one with its error, which the API replaces with a note for a reader without jobs.view), or after the
 * process stopped during an attempt (no error, attempts or retries left from before).
 */
export const retrying = (job: Job) =>
  job.status === 'queued' && (job.error !== '' || job.attempts > 0 || job.retries > 0)

/**
 * When a retrying job runs again: "15:04 重試，已連不上伺服器 3 次", the error saying what failed last; or, with no
 * error, "上次執行中斷，即將繼續".
 */
export function retryText(job: Job) {
  const when = isPast(job.runAfter) ? '即將' : `${formatTime(job.runAfter, { hour: '2-digit', minute: '2-digit' })} `
  if (!job.error) return `上次執行中斷，${when}繼續`
  return `${when}重試${job.retries > 0 ? `，已連不上伺服器 ${job.retries} 次` : ''}`
}

/** The stage a job is at: "Parse files and symbols", or "Wiki queued" before it starts, "Waiting to retry" after. */
export function stageName(job: Job) {
  if (job.status === 'queued')
    return retrying(job) ? (job.error ? '等待重試' : '等待繼續') : job.kind === 'wiki' ? 'Wiki 排隊中' : '索引排隊中'
  return STAGES[job.kind][stageIndex(job)][1]
}

/** How far into its stage a job is, "120 / 900"; empty while the stage is not counted. */
export const stageCount = (job: Job) =>
  job.status !== 'queued' && job.total > 0 ? `${job.progress} / ${job.total}` : ''

/** One line for small spaces: "Parse files and symbols 120 / 900", or why and when a retrying job runs again. */
export const stageLabel = (job: Job) => (retrying(job) ? retryText(job) : `${stageName(job)} ${stageCount(job)}`.trim())

/** A job in a list: its error once failed, done, or its stage. */
export const stageText = (job: Job) =>
  job.status === 'failed' ? job.error || '失敗' : job.status === 'succeeded' ? '完成' : stageLabel(job)

export const isActive = (job: Job | null | undefined) => job?.status === 'queued' || job?.status === 'running'

/** Indexed, with no wiki yet: the wiki job is queued just after the snapshot is published, so look again shortly. */
export const awaitingWiki = (repo: Repo) =>
  repo.job?.kind === 'index' && repo.job.status === 'succeeded' && repo.snapshot?.wikiStatus === 'none'
