import type { Health, Job, Repo, ServerOption } from '../../types/repo/repo'
import { api, del, post } from '../client'

export const getHealth = () => api<Health>('/api/health')

/** The repositories the caller may see. */
export const listRepos = () => api<Repo[]>('/api/repos')

export const getRepo = (id: string) => api<Repo>(`/api/repos/${id}`)

/** The git servers to import from. */
export const listServers = () => api<ServerOption[]>('/api/servers')

/** url: owner/name, a web URL, or a path on serverId; answers the queued (or the already imported) repository, without permissions. */
export const importRepo = (url: string, serverId?: number) =>
  post<Omit<Repo, 'permissions'>>('/api/repos', { url, serverId })

/** Indexes the latest commit; upToDate means there is nothing new. */
export const syncRepo = (repoId: string) => post<{ upToDate?: boolean; job?: Job }>(`/api/repos/${repoId}/sync`, {})

/** The remote's branches and the one the repository follows. */
export const listBranches = (repoId: string) =>
  api<{ branches: string[]; trackedBranch: string; defaultBranch: string }>(`/api/repos/${repoId}/branches`)

/** Makes the repository follow branch ("" its default) and syncs it. */
export const setBranch = (repoId: string, branch: string) =>
  post<{ upToDate?: boolean; job?: Job }>(`/api/repos/${repoId}/branch`, { branch })

/** Whether a webhook secret is set. */
export const getWebhook = (repoId: string) => api<{ hasSecret: boolean }>(`/api/repos/${repoId}/webhook`)

/** Sets a webhook secret (empty generates one) and returns it once. */
export const setWebhook = (repoId: string) => post<{ secret: string }>(`/api/repos/${repoId}/webhook`, {})

export const clearWebhook = (repoId: string) => del(`/api/repos/${repoId}/webhook`)
