import type { Permission } from '../auth'
import type { Provider } from '../repo/repo'

export type PermissionInfo = { name: Permission; description: string }

export type Role = {
  id: number
  name: string
  description: string
  builtin: boolean
  permissions: Permission[]
  allRepositories: boolean
  repositoryIds: string[]
  userCount: number
}

export type RoleInput = Pick<Role, 'name' | 'description' | 'permissions' | 'allRepositories' | 'repositoryIds'>

/** A repository a role can be limited to; updatedAt is when it was last indexed (SQLite UTC), "" if never. */
export type RepoRef = {
  id: string
  owner: string
  name: string
  provider: Provider
  serverName: string
  updatedAt: string
}
