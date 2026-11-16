type RoleRef = { id: string; name: string }

export type User = {
  id: string
  username: string
  displayName: string
  disabled: boolean
  roles: RoleRef[]
  createdAt: string
}

export type NewUser = { username: string; displayName: string; password: string; roleIds: string[] }

/** Only the fields present change; no password keeps the current one. */
export type UserChanges = { displayName?: string; password?: string; disabled?: boolean; roleIds?: string[] }
