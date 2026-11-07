import type { Provider } from '../repo/repo'

export type GitServer = {
  id: number
  name: string
  kind: Provider
  baseUrl: string
  apiUrl: string // effective: the override, else the one derived from baseUrl
  hasToken: boolean // with OAuth: authorized
  repositoryCount: number
  createdAt: string
  clientId: string // the OAuth app the token is granted to; '' when the token is pasted
  hasClientSecret: boolean
}

export type NewGitServer = {
  name: string
  kind: Provider
  baseUrl: string
  apiUrl?: string
  token?: string
  clientId?: string
  clientSecret?: string
}

/** Only the fields present change; clearToken removes the stored token (with OAuth, ends the authorization); clientId '' goes back to a pasted token. */
export type GitServerChanges = Partial<NewGitServer> & { clearToken?: boolean }

export type ConnectionTest = { ok: boolean; detail: string }
