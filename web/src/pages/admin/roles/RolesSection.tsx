import { PencilIcon, PlusIcon, Trash2Icon } from 'lucide-react'
import { deleteRole, listPermissions, listRepositories, listRoles } from '../../../api/admin/roles'
import { Button } from '../../../components/ui/controls/button'
import { Badge } from '../../../components/ui/display/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../../components/ui/display/table'
import { LoadError } from '../../../components/ui/feedback/load-error'
import { SkeletonText } from '../../../components/ui/feedback/skeleton'
import { ConfirmDialog } from '../../../components/ui/overlay/confirm-dialog'
import { plural } from '../../../lib/format'
import { useApi } from '../../../lib/useApi'
import { useDialog } from '../../../lib/useDialog'
import type { Role } from '../../../types/admin/roles'
import { useSession } from '../../session/session'
import { SectionHead } from '../SectionHead'
import { RoleDialog } from './RoleDialog'

/** Roles: a set of permissions over all repositories or a chosen few. */
export function RolesSection() {
  const { refresh } = useSession()
  const { data: roles, error, reload } = useApi('admin:roles', listRoles)
  const {
    data: permissions,
    error: permissionsError,
    reload: reloadPermissions,
  } = useApi('admin:permissions', listPermissions)
  const { data: repos, error: reposError, reload: reloadRepos } = useApi('admin:repositories', listRepositories)
  const failed = error || permissionsError || reposError
  const lists = permissions && repos ? { permissions, repos } : null // the role form is offered only with both
  const edit = useDialog<Role | 'new'>()
  const remove = useDialog<Role>()
  const removing = remove.item
  // A role you hold may have changed: the header and what you may open follow it.
  const changed = () => {
    reload()
    refresh().catch(() => {})
  }
  return (
    <section>
      <SectionHead title="Roles">
        <Button disabled={!lists} onClick={() => edit.show('new')}>
          <PlusIcon /> New role
        </Button>
      </SectionHead>
      <LoadError
        error={failed}
        onRetry={() => {
          if (error) reload()
          if (permissionsError) reloadPermissions()
          if (reposError) reloadRepos()
        }}
      />
      {!roles ? (
        !error && <SkeletonText />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Description</TableHead>
              <TableHead>Permissions</TableHead>
              <TableHead>Repositories</TableHead>
              <TableHead>Users</TableHead>
              <TableHead>
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {roles.map((r) => (
              <TableRow key={r.id}>
                <TableCell className="font-medium">
                  <span className="inline-flex items-center gap-2">
                    {r.name} {r.builtin && <Badge variant="outline">Built-in</Badge>}
                  </span>
                </TableCell>
                <TableCell>
                  <span className="block max-w-[200px] truncate text-muted-foreground" title={r.description}>
                    {r.description}
                  </span>
                </TableCell>
                <TableCell title={r.permissions.join(', ')}>{plural(r.permissions.length, 'permission')}</TableCell>
                <TableCell>
                  {r.allRepositories
                    ? 'All repositories'
                    : plural(r.repositoryIds.length, 'repository', 'repositories')}
                </TableCell>
                <TableCell>{r.userCount}</TableCell>
                <TableCell className="text-right">
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={`Edit ${r.name}`}
                    disabled={!lists}
                    onClick={() => edit.show(r)}
                  >
                    <PencilIcon />
                  </Button>
                  {!r.builtin && (
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      aria-label={`Delete ${r.name}`}
                      onClick={() => remove.show(r)}
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
      {edit.item && lists && (
        <RoleDialog
          key={edit.key}
          open={edit.open}
          role={edit.item === 'new' ? undefined : edit.item}
          {...lists}
          onClose={edit.hide}
          onSaved={changed}
        />
      )}
      {removing && (
        <ConfirmDialog
          key={remove.key}
          open={remove.open}
          title={`Delete the ${removing.name} role?`}
          confirm="Delete role"
          description={`${removing.userCount ? `${plural(removing.userCount, 'user')} will lose what this role grants. ` : ''}This cannot be undone.`}
          onClose={remove.hide}
          onConfirm={() => deleteRole(removing.id).then(changed)}
        />
      )}
    </section>
  )
}
