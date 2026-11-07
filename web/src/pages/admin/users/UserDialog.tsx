import { useState } from 'react'
import { createUser, updateUser } from '../../../api/admin/users'
import { Checkbox } from '../../../components/ui/controls/checkbox'
import { Switch } from '../../../components/ui/controls/switch'
import { Field } from '../../../components/ui/form/field'
import { Input } from '../../../components/ui/form/input'
import { Label } from '../../../components/ui/form/label'
import { FormDialog } from '../../../components/ui/overlay/form-dialog'
import type { Role } from '../../../types/admin/roles'
import type { User } from '../../../types/admin/users'

/** Creates a user, or edits one (no username change; a blank password keeps the current one; self: nobody disables themselves). */
export function UserDialog({
  open,
  user,
  self,
  roles,
  onClose,
  onSaved,
}: {
  open: boolean
  user?: User
  self?: boolean
  roles: Role[]
  onClose: () => void
  onSaved: () => void
}) {
  const [username, setUsername] = useState(user?.username ?? '')
  const [displayName, setDisplayName] = useState(user?.displayName ?? '')
  const [password, setPassword] = useState('')
  const [roleIds, setRoleIds] = useState(() => user?.roles.map((r) => r.id) ?? [])
  const [disabled, setDisabled] = useState(user?.disabled ?? false)
  const toggle = (id: number) => setRoleIds((ids) => (ids.includes(id) ? ids.filter((x) => x !== id) : [...ids, id]))
  const save = async () => {
    if (user) await updateUser(user.id, { displayName, roleIds, disabled, ...(password ? { password } : {}) })
    else await createUser({ username, displayName, password, roleIds })
    onSaved()
  }
  return (
    <FormDialog
      open={open}
      title={user ? `編輯 ${user.username}` : '新增使用者'}
      submit={user ? '儲存' : '建立使用者'}
      onClose={onClose}
      onSubmit={save}
    >
      {!user && (
        <Field label="使用者名稱">
          <Input
            required
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            value={username}
            onChange={(e) => setUsername(e.target.value)}
          />
        </Field>
      )}
      <Field label="顯示名稱">
        <Input autoComplete="off" value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
      </Field>
      <Field label={user ? '新密碼' : '密碼'}>
        <Input
          type="password"
          autoComplete="new-password"
          required={!user}
          minLength={8}
          value={password}
          placeholder={user ? '留空則保留目前的密碼' : undefined}
          onChange={(e) => setPassword(e.target.value)}
        />
      </Field>
      <fieldset>
        <legend className="text-sm font-medium">角色</legend>
        <div className="mt-2 grid gap-2.5">
          {roles.map((r) => (
            <Label key={r.id} className="items-start leading-snug font-normal">
              <Checkbox className="mt-0.5" checked={roleIds.includes(r.id)} onCheckedChange={() => toggle(r.id)} />
              <span>
                {r.name}
                {r.description && <span className="text-muted-foreground"> {r.description}</span>}
              </span>
            </Label>
          ))}
        </div>
      </fieldset>
      {user && !self && (
        <Label className="font-normal">
          <Switch checked={disabled} onCheckedChange={setDisabled} />
          停用：無法登入
        </Label>
      )}
    </FormDialog>
  )
}
