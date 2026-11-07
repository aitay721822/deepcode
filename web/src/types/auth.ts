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
