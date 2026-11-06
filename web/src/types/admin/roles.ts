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
  repositoryIds: number[]
  userCount: number
}

export type RoleInput = Pick<Role, 'name' | 'description' | 'permissions' | 'allRepositories' | 'repositoryIds'>

/** A repository a role can be limited to. */
export type RepoRef = { id: number; owner: string; name: string; provider: Provider; serverName: string }
