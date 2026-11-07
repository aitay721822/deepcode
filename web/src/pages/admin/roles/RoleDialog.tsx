import { useState } from 'react'
import { createRole, updateRole } from '../../../api/admin/roles'
import { Checkbox } from '../../../components/ui/controls/checkbox'
import { Switch } from '../../../components/ui/controls/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '../../../components/ui/display/tabs'
import { Field } from '../../../components/ui/form/field'
import { Input } from '../../../components/ui/form/input'
import { Label } from '../../../components/ui/form/label'
import { FormDialog } from '../../../components/ui/overlay/form-dialog'
import type { PermissionInfo, RepoRef, Role } from '../../../types/admin/roles'
import type { Permission } from '../../../types/auth'
import { RepoPicker } from './RepoPicker'

/**
 * Creates or edits a role: name, then on two tabs its permissions and its repositories (all, or a chosen few). What the server refuses
 * is locked: a built-in role's name, and the admin role's grants (every permission on every repository).
 */
export function RoleDialog({
  open,
  role,
  permissions,
  repos,
  onClose,
  onSaved,
}: {
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
  const [tab, setTab] = useState('permissions')
  const admin = role?.builtin && role.name === 'admin'
  const toggle = (p: Permission) => setGranted((ps) => (ps.includes(p) ? ps.filter((x) => x !== p) : [...ps, p]))
  const save = async () => {
    const live = repoIds.filter((id) => repos.some((r) => r.id === id)) // not those deleted since
    const input = { name, description, permissions: granted, allRepositories: all, repositoryIds: all ? [] : live }
    await (role ? updateRole(role.id, input) : createRole(input))
    onSaved()
  }
  return (
    <FormDialog
      open={open}
      title={role ? `編輯 ${role.name}` : '新增角色'}
      submit={role ? '儲存' : '建立角色'}
      onClose={onClose}
      onSubmit={save}
    >
      <Field label="名稱" hint={role?.builtin ? '內建角色無法更名。' : undefined}>
        <Input
          required
          autoComplete="off"
          value={name}
          disabled={role?.builtin}
          onChange={(e) => setName(e.target.value)}
        />
      </Field>
      <Field label="說明">
        <Input autoComplete="off" value={description} onChange={(e) => setDescription(e.target.value)} />
      </Field>
      {admin && <p className="text-xs text-muted-foreground">admin 角色一律擁有所有 Repository 的所有權限。</p>}
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="permissions">權限</TabsTrigger>
          <TabsTrigger value="repos">Repository</TabsTrigger>
        </TabsList>
        {/* As tall as the longer tab, so the dialog does not jump between them. */}
        <TabsContent value="permissions" className="min-h-80">
          <fieldset>
            <legend className="sr-only">權限</legend>
            <div className="grid gap-2.5">
              {permissions.map((p) => (
                <Label key={p.name} className="items-start leading-snug font-normal">
                  <Checkbox
                    className="mt-0.5"
                    checked={granted.includes(p.name)}
                    disabled={admin}
                    onCheckedChange={() => toggle(p.name)}
                  />
                  <span>
                    <code className="font-mono text-[12.5px]">{p.name}</code>{' '}
                    <span className="text-muted-foreground">{p.description}</span>
                  </span>
                </Label>
              ))}
            </div>
          </fieldset>
        </TabsContent>
        <TabsContent value="repos" className="grid min-h-80 content-start gap-4">
          <Label className="font-normal">
            <Switch checked={all} disabled={admin} onCheckedChange={setAll} />
            所有 Repository，包括日後新增的
          </Label>
          {!all && <RepoPicker repos={repos} value={repoIds} onChange={setRepoIds} />}
        </TabsContent>
      </Tabs>
    </FormDialog>
  )
}
