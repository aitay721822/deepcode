import { useState } from 'react'
import { Field } from '../ui/form/field'
import { Input } from '../ui/form/input'
import { FormDialog } from '../ui/overlay/form-dialog'

/** Asks for the current password and a new one twice; onSubmit's error (e.g. a wrong current password) shows in the dialog. */
export function PasswordDialog({
  open,
  username,
  onClose,
  onSubmit,
}: {
  open: boolean
  username: string
  onClose: () => void
  onSubmit: (current: string, next: string) => Promise<unknown>
}) {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  return (
    <FormDialog
      open={open}
      title="變更密碼"
      submit="變更密碼"
      onClose={onClose}
      onSubmit={() =>
        next === confirm ? onSubmit(current, next) : Promise.reject(new Error('兩次輸入的新密碼不一致。'))
      }
    >
      <input type="text" name="username" autoComplete="username" value={username} readOnly hidden />
      <Field label="目前密碼">
        <Input
          type="password"
          autoComplete="current-password"
          required
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
        />
      </Field>
      <Field label="新密碼">
        <Input
          type="password"
          autoComplete="new-password"
          required
          minLength={8}
          value={next}
          onChange={(e) => setNext(e.target.value)}
        />
      </Field>
      <Field label="確認新密碼">
        <Input
          type="password"
          autoComplete="new-password"
          required
          minLength={8}
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
        />
      </Field>
    </FormDialog>
  )
}
