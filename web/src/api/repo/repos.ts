import type { Health, Job, Repo, ServerOption } from '../../types/repo/repo'
import { api, post } from '../client'

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
