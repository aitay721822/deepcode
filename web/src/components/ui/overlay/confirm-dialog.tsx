import { type ReactNode, useState } from 'react'
import { Button } from '../controls/button'
import { Spinner } from '../feedback/spinner'
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from './alert-dialog'

/**
 * Asks before a destructive step. Cancel has focus first; the action stays busy until onConfirm settles,
 * then closes the dialog or shows why it failed. Mount it anew for each opening (useDialog's key).
 */
export function ConfirmDialog({
  open,
  title,
  description,
  confirm,
  onClose,
  onConfirm,
}: {
  open: boolean
  title: string
  description: ReactNode
  confirm: string
  onClose: () => void
  onConfirm: () => Promise<unknown>
}) {
  const [opener] = useState(() => document.activeElement as HTMLElement | null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const close = () => busy || onClose()
  return (
    <AlertDialog open={open} onOpenChange={(o) => o || close()}>
      <AlertDialogContent
        onCloseAutoFocus={(e) => {
          e.preventDefault()
          opener?.focus()
        }}
      >
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        {error && (
          <p className="text-sm text-destructive" role="alert">
            {error}
          </p>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>取消</AlertDialogCancel>
          <Button
            variant="destructive"
            busy={busy}
            onClick={async () => {
              setBusy(true)
              setError('')
              try {
                await onConfirm()
                onClose()
              } catch (err) {
                setError((err as Error).message)
                setBusy(false)
              }
            }}
          >
            {busy && <Spinner />}
            {confirm}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
