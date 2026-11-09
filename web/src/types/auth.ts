export type Permission =
  | 'repo.read'
  | 'repo.chat'
  | 'repo.import'
  | 'repo.sync'
  | 'jobs.view'
  | 'users.manage'
  | 'servers.manage'

/** Whether visitors may create their own account; an administrator opens sign-up. */
export type SignUpState = { open: boolean }

/** The signed-in user. */
export type Me = {
  id: number
  username: string
  displayName: string
  permissions: Permission[]
  allRepositories: boolean
}

/** A personal access token, as its owner sees it: never its secret. expiresAt null: until revoked. */
export type Token = { id: number; name: string; createdAt: string; lastUsedAt: string | null; expiresAt: string | null }

/** A token just created, with its secret, which is shown this once. */
export type NewToken = { token: Token; secret: string }

/** An app (an MCP client) the user lets act for them through OAuth. */
export type App = { clientId: string; name: string; authorizedAt: string; lastUsedAt: string | null }

/** An authorization request, as an app sent it to /oauth/authorize. */
export type AuthRequest = {
  responseType: string
  clientId: string
  redirectUri: string
  codeChallenge: string
  codeChallengeMethod: string
  state: string
}
