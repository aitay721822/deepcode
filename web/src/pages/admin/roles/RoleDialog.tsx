import { useState } from 'react'
import { createRole, updateRole } from '../../../api/admin/roles'
import { Checkbox } from '../../../components/ui/controls/checkbox'
import { Switch } from '../../../components/ui/controls/switch'
import { Field } from '../../../components/ui/form/field'
import { Input } from '../../../components/ui/form/input'
import { Label } from '../../../components/ui/form/label'
import { FormDialog } from '../../../components/ui/overlay/form-dialog'
import type { PermissionInfo, RepoRef, Role } from '../../../types/admin/roles'
import type { Permission } from '../../../types/auth'
import { RepoPicker } from './RepoPicker'

/**
 * Creates or edits a role: name, permissions, and all repositories or a chosen few. What the server refuses
 * is locked: a built-in role's name, and the admin role's grants (every permission on every repository).
 */
export function RoleDialog({ open, role, permissions, repos, onClose, onSaved }: {
  open: boolean
  role?: Role
  permissions: PermissionInfo[]
  repos: RepoRef[]
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState(role?.name ?? '')
  const [description, setDescription] = useState(role?.description ?? '')
  const [granted, setGranted] = useState<Permission[]>(role?.permissions ?? [])
  const [all, setAll] = useState(role?.allRepositories ?? false)
  const [repoIds, setRepoIds] = useState(role?.repositoryIds ?? [])
  const admin = role?.builtin && role.name === 'admin'
  const toggle = (p: Permission) => setGranted((ps) => (ps.includes(p) ? ps.filter((x) => x !== p) : [...ps, p]))
  const save = async () => {
    const input = { name, description, permissions: granted, allRepositories: all, repositoryIds: all ? [] : repoIds }
    await (role ? updateRole(role.id, input) : createRole(input))
    onSaved()
  }
  return (
    <FormDialog open={open} title={role ? `Edit ${role.name}` : 'New role'} submit={role ? 'Save' : 'Create role'} onClose={onClose} onSubmit={save}>
      <Field label="Name" hint={role?.builtin ? 'Built-in roles keep their name.' : undefined}>
        <Input required autoComplete="off" value={name} disabled={role?.builtin} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label="Description">
        <Input autoComplete="off" value={description} onChange={(e) => setDescription(e.target.value)} />
      </Field>
      <fieldset>
        <legend className="text-sm font-medium">Permissions</legend>
        {admin && <p className="mt-1 text-xs text-muted-foreground">The admin role always holds every permission on every repository.</p>}
        <div className="mt-2 grid gap-2.5">
          {permissions.map((p) => (
            <Label key={p.name} className="items-start leading-snug font-normal">
              <Checkbox className="mt-0.5" checked={granted.includes(p.name)} disabled={admin} onCheckedChange={() => toggle(p.name)} />
              <span><code className="font-mono text-[12.5px]">{p.name}</code> <span className="text-muted-foreground">{p.description}</span></span>
            </Label>
          ))}
        </div>
      </fieldset>
      <Label className="font-normal">
        <Switch checked={all} disabled={admin} onCheckedChange={setAll} />
        All repositories, including ones added later
      </Label>
      {!all && <RepoPicker repos={repos} value={repoIds} onChange={setRepoIds} />}
    </FormDialog>
  )
}
