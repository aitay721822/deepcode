import type { NewUser, User, UserChanges } from '../../types/admin/users'
import type { SignUpState } from '../../types/auth'
import { api, del, patch, post } from '../client'

export const listUsers = () => api<User[]>('/api/admin/users')

export const createUser = (user: NewUser) => post<User>('/api/admin/users', user)

export const updateUser = (id: number, changes: UserChanges) => patch<User>(`/api/admin/users/${id}`, changes)

/** Opens or closes sign-up. */
export const setSignUp = (open: boolean) => patch<SignUpState>('/api/admin/signup', { open })

/** Fails with 409 when it would remove the last administrator. */
export const deleteUser = (id: number) => del(`/api/admin/users/${id}`)
