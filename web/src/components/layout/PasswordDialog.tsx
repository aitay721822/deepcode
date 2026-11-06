import { useState } from 'react'
import { Field } from '../ui/form/field'
import { Input } from '../ui/form/input'
import { FormDialog } from '../ui/overlay/form-dialog'

/** Asks for the current password and a new one twice; onSubmit's error (e.g. a wrong current password) shows in the dialog. */
export function PasswordDialog({ open, username, onClose, onSubmit }: {
  open: boolean
  username: string
  onClose: () => void
  onSubmit: (current: string, next: string) => Promise<unknown>
}) {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  return (
    <FormDialog open={open} title="Change password" submit="Change password" onClose={onClose}
      onSubmit={() => (next === confirm ? onSubmit(current, next) : Promise.reject(new Error('The new passwords do not match.')))}>
      <input type="text" name="username" autoComplete="username" value={username} readOnly hidden />
      <Field label="Current password">
        <Input type="password" autoComplete="current-password" required value={current} onChange={(e) => setCurrent(e.target.value)} />
      </Field>
      <Field label="New password">
        <Input type="password" autoComplete="new-password" required minLength={8} value={next} onChange={(e) => setNext(e.target.value)} />
      </Field>
      <Field label="Confirm new password">
        <Input type="password" autoComplete="new-password" required minLength={8} value={confirm} onChange={(e) => setConfirm(e.target.value)} />
      </Field>
    </FormDialog>
  )
}
