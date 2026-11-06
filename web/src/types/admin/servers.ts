import type { Provider } from '../repo/repo'

export type GitServer = {
  id: number
  name: string
  kind: Provider
  baseUrl: string
  apiUrl: string // effective: the override, else the one derived from baseUrl
  hasToken: boolean
  repositoryCount: number
  createdAt: string
}

export type NewGitServer = { name: string; kind: Provider; baseUrl: string; apiUrl?: string; token?: string }

/** Only the fields present change; clearToken removes the stored token. */
export type GitServerChanges = Partial<NewGitServer> & { clearToken?: boolean }

export type ConnectionTest = { ok: boolean; detail: string }
