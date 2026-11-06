import type { PermissionInfo, RepoRef, Role, RoleInput } from '../../types/admin/roles'
import { api, del, patch, post } from '../client'

export const listPermissions = () => api<PermissionInfo[]>('/api/admin/permissions')

export const listRoles = () => api<Role[]>('/api/admin/roles')

export const createRole = (role: RoleInput) => post<Role>('/api/admin/roles', role)

export const updateRole = (id: number, changes: Partial<RoleInput>) => patch<Role>(`/api/admin/roles/${id}`, changes)

/** Fails with 409 for a built-in role. */
export const deleteRole = (id: number) => del(`/api/admin/roles/${id}`)

/** Every repository, for limiting a role to some of them. */
export const listRepositories = () => api<RepoRef[]>('/api/admin/repositories')
