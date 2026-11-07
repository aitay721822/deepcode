import { PencilIcon, PlusIcon, Trash2Icon } from 'lucide-react'
import { listRoles } from '../../../api/admin/roles'
import { deleteUser, listUsers } from '../../../api/admin/users'
import { Button } from '../../../components/ui/controls/button'
import { Badge } from '../../../components/ui/display/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../../components/ui/display/table'
import { LoadError } from '../../../components/ui/feedback/load-error'
import { SkeletonText } from '../../../components/ui/feedback/skeleton'
import { ConfirmDialog } from '../../../components/ui/overlay/confirm-dialog'
import { formatDate } from '../../../lib/format'
import { useApi } from '../../../lib/useApi'
import { useDialog } from '../../../lib/useDialog'
import type { User } from '../../../types/admin/users'
import { useSession } from '../../session/session'
import { SectionHead } from '../SectionHead'
import { SignUpSwitch } from './SignUpSwitch'
import { UserDialog } from './UserDialog'

/** Accounts: who they are, their roles; create, edit, disable, delete (not yourself); and whether visitors may sign up. */
export function UsersSection() {
  const { user: me, refresh } = useSession()
  const { data: users, error, reload } = useApi('admin:users', listUsers)
  const { data: roles, error: rolesError, reload: reloadRoles } = useApi('admin:roles', listRoles) // the user form is offered only with them
  const failed = error || rolesError
  const edit = useDialog<User | 'new'>()
  const remove = useDialog<User>()
  const editing = edit.item === 'new' ? undefined : edit.item
  const removing = remove.item
  const saved = () => {
    reload()
    if (editing?.id === me?.id) refresh().catch(() => {}) // your own account: the header and what you may open follow it
  }
  return (
    <section>
      <SectionHead title="Users">
        <Button disabled={!roles} onClick={() => edit.show('new')}>
          <PlusIcon /> New user
        </Button>
      </SectionHead>
      <SignUpSwitch />
      <LoadError
        error={failed}
        onRetry={() => {
          if (error) reload()
          if (rolesError) reloadRoles()
        }}
      />
      {!users ? (
        !error && <SkeletonText />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Username</TableHead>
              <TableHead>Display name</TableHead>
              <TableHead>Roles</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Created</TableHead>
              <TableHead>
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {users.map((u) => (
              <TableRow key={u.id}>
                <TableCell className="font-medium">{u.username}</TableCell>
                <TableCell dir="auto">{u.displayName}</TableCell>
                <TableCell>
                  <span className="inline-flex flex-wrap gap-1">
                    {u.roles.map((r) => (
                      <Badge key={r.id}>{r.name}</Badge>
                    ))}
                  </span>
                </TableCell>
                <TableCell>
                  {u.disabled ? (
                    <Badge variant="outline" className="border-destructive/50 text-destructive">
                      Disabled
                    </Badge>
                  ) : (
                    'Active'
                  )}
                </TableCell>
                <TableCell>{formatDate(u.createdAt)}</TableCell>
                <TableCell className="text-right">
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={`Edit ${u.username}`}
                    disabled={!roles}
                    onClick={() => edit.show(u)}
                  >
                    <PencilIcon />
                  </Button>
                  {u.id !== me?.id && (
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      aria-label={`Delete ${u.username}`}
                      onClick={() => remove.show(u)}
                    >
                      <Trash2Icon />
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {edit.item && roles && (
        <UserDialog
          key={edit.key}
          open={edit.open}
          user={editing}
          self={editing?.id === me?.id}
          roles={roles}
          onClose={edit.hide}
          onSaved={saved}
        />
      )}
      {removing && (
        <ConfirmDialog
          key={remove.key}
          open={remove.open}
          title={`Delete ${removing.username}?`}
          confirm="Delete user"
          description="The account can no longer sign in. This cannot be undone."
          onClose={remove.hide}
          onConfirm={() => deleteUser(removing.id).then(reload)}
        />
      )}
    </section>
  )
}
