import type { App, AuthRequest, Me, NewToken, SignUpState, Token } from '../types/auth'
import { api, del, post } from './client'

export const getMe = () => api<Me>('/api/auth/me')

/** Signs in (the server sets an HttpOnly session cookie). */
export const login = (username: string, password: string) => post<Me>('/api/auth/login', { username, password })

export const getSignUp = () => api<SignUpState>('/api/auth/signup')

/** Creates an account with the member role and signs it in, like login; displayName may be empty (the username then). */
export const signUp = (username: string, displayName: string, password: string) =>
  post<Me>('/api/auth/signup', { username, displayName, password })

export const logout = () => post<unknown>('/api/auth/logout', {})

export const changePassword = (current: string, next: string) => post<unknown>('/api/auth/password', { current, next })

export const listTokens = () => api<Token[]>('/api/auth/tokens')

/** days 0: until revoked. The answer holds the secret, which the server never shows again. */
export const createToken = (name: string, days: number) => post<NewToken>('/api/auth/tokens', { name, days })

export const revokeToken = (id: number) => del(`/api/auth/tokens/${id}`)

export const listApps = () => api<App[]>('/api/auth/apps')

export const revokeApp = (clientId: string) => del(`/api/auth/apps/${encodeURIComponent(clientId)}`)

/** The app an authorization request comes from, while its redirect URI is one the app registered. */
export const getAuthApp = (clientId: string, redirectUri: string) =>
  api<{ name: string }>(
    `/api/oauth/authorize?${new URLSearchParams({ client_id: clientId, redirect_uri: redirectUri })}`,
  )

/** Approves or declines an authorization request: where to send the browser back to, the app's address. */
export const answerAuth = (request: AuthRequest, approve: boolean) =>
  post<{ redirect: string }>('/api/oauth/authorize', { ...request, approve })
