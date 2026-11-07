import { changePassword } from '../api/auth'
import { AccountMenu } from '../components/layout/AccountMenu'
import { Header } from '../components/layout/Header'
import { PasswordDialog } from '../components/layout/PasswordDialog'
import { useDialog } from '../lib/useDialog'
import type { Repo } from '../types/repo/repo'
import { useSession } from './session/session'

/** The header every page shows, with the signed-in user's menu. */
export function SiteHeader({ repo }: { repo?: Repo }) {
  const { user, can, logout } = useSession()
  const password = useDialog<true>()
  return (
    <Header repo={repo}>
      {user && (
        <AccountMenu
          name={user.displayName || user.username}
          admin={can('jobs.view') || can('users.manage') || can('servers.manage')}
          onPassword={() => password.show(true)}
          onSignOut={logout}
        />
      )}
      {user && password.item && (
        <PasswordDialog
          key={password.key}
          open={password.open}
          username={user.username}
          onClose={password.hide}
          onSubmit={changePassword}
        />
      )}
    </Header>
  )
}
