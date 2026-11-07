import type { Me, SignUpState } from '../types/auth'
import { api, post } from './client'

export const getMe = () => api<Me>('/api/auth/me')

/** Signs in (the server sets an HttpOnly session cookie). */
export const login = (username: string, password: string) => post<Me>('/api/auth/login', { username, password })

export const getSignUp = () => api<SignUpState>('/api/auth/signup')

/** Creates an account with the member role and signs it in, like login; displayName may be empty (the username then). */
export const signUp = (username: string, displayName: string, password: string) =>
  post<Me>('/api/auth/signup', { username, displayName, password })

export const logout = () => post<unknown>('/api/auth/logout', {})

export const changePassword = (current: string, next: string) => post<unknown>('/api/auth/password', { current, next })
