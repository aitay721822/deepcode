import { type ReactNode, useState } from 'react'
import { Button } from '../controls/button'
import { Spinner } from '../feedback/spinner'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from './dialog'

/**
 * A modal form. Escape, the backdrop, the close button and Cancel close it, except while it saves; a rejected
 * onSubmit shows its message and keeps it open, a resolved one closes it. Mount it anew for each opening
 * (useDialog's key): it hands focus back to whatever had it when it opened.
 */
export function FormDialog({
  open,
  title,
  submit,
  onClose,
  onSubmit,
  children,
}: {
  open: boolean
  title: string
  submit: string
  onClose: () => void
  onSubmit: () => Promise<unknown>
  children?: ReactNode
}) {
  const [opener] = useState(() => document.activeElement as HTMLElement | null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const close = () => busy || onClose()
  return (
    <Dialog open={open} onOpenChange={(o) => o || close()}>
      <DialogContent
        aria-describedby={undefined}
        onCloseAutoFocus={(e) => {
          e.preventDefault()
          opener?.focus()
        }}
      >
        <form
          className="grid gap-4"
          onSubmit={async (e) => {
            e.preventDefault()
            setBusy(true)
            setError('')
            try {
              await onSubmit()
              onClose()
            } catch (err) {
              setError((err as Error).message)
              setBusy(false)
            }
          }}
        >
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
          </DialogHeader>
          {children}
          {error && (
            <p className="text-sm text-destructive" role="alert">
              {error}
            </p>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={close}>
              Cancel
            </Button>
            <Button type="submit" busy={busy}>
              {busy && <Spinner />}
              {submit}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
