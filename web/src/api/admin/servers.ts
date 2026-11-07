import type { ConnectionTest, GitServer, GitServerChanges, NewGitServer } from '../../types/admin/servers'
import { api, del, patch, post } from '../client'

export const listGitServers = () => api<GitServer[]>('/api/admin/servers')

export const createGitServer = (server: NewGitServer) => post<GitServer>('/api/admin/servers', server)

export const updateGitServer = (id: number, changes: GitServerChanges) =>
  patch<GitServer>(`/api/admin/servers/${id}`, changes)

/** Fails with 409 while repositories still use the server. */
export const deleteGitServer = (id: number) => del(`/api/admin/servers/${id}`)

export const testGitServer = (id: number) => post<ConnectionTest>(`/api/admin/servers/${id}/test`, {})
