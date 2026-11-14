import type { Permission } from '../auth'

export type Provider = 'github' | 'gitlab'

export type Job = {
  id: number
  kind: 'index' | 'wiki'
  commitSha: string
  status: 'queued' | 'running' | 'succeeded' | 'failed'
  attempts: number
  retries: number // the runs in a row cut by a server it could not reach
  stage: string
  progress: number
  total: number
  error: string
  runAfter: string // when a queued job may start: after a retry's backoff
  createdAt: string
  updatedAt: string
}

type Snapshot = {
  id: number
  commitSha: string
  indexedAt: string
  wikiStatus: 'none' | 'generating' | 'ready' | 'failed'
  embeddingModel: string
  fileCount: number
  chunkCount: number
}

export type Repo = {
  id: string // a CUID2, never the database's row id
  owner: string // may hold "/" (GitLab subgroups)
  name: string
  url: string // web URL, no .git
  provider: Provider
  serverId: number
  serverName: string
  description: string
  stars: number
  defaultBranch: string
  trackedBranch: string // "" follows defaultBranch
  snapshot: Snapshot | null
  job: Job | null
  permissions: Permission[] // what the reader may do on this repository: repo.read, repo.chat, repo.sync
}

/** A git server repositories can be imported from. */
export type ServerOption = { id: number; name: string; kind: Provider; baseUrl: string }

export type Health = { status: 'ok'; chatModel: string; embeddingModel: string }
