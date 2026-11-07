import { useId, useState } from 'react'
import { setSignUp } from '../../../api/admin/users'
import { getSignUp } from '../../../api/auth'
import { Switch } from '../../../components/ui/controls/switch'
import { LoadError } from '../../../components/ui/feedback/load-error'
import { Label } from '../../../components/ui/form/label'
import { useApi } from '../../../lib/useApi'

/** Whether visitors may create their own account, and what such an account gets. */
export function SignUpSwitch() {
  const id = useId()
  const { data, error: loadError, reload } = useApi('signup', getSignUp)
  const [saved, setSaved] = useState<boolean>() // the answer to the last change made here
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const open = saved ?? data?.open
  const change = async (next: boolean) => {
    setBusy(true)
    setError('')
    try {
      setSaved((await setSignUp(next)).open)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  // While a change is saved the switch ignores presses but keeps focus, which disabled would drop to the page.
  return (
    <div className="mb-6">
      <Label className="items-start leading-snug font-normal">
        <Switch
          className="mt-0.5"
          aria-labelledby={`${id}-name`}
          aria-describedby={`${id}-note`}
          checked={!!open}
          disabled={open === undefined}
          aria-busy={busy || undefined}
          onCheckedChange={(next) => busy || change(next)}
        />
        <span>
          <span id={`${id}-name`}>Open sign-up</span>{' '}
          <span id={`${id}-note`} className="text-muted-foreground">
            Anyone who can reach this site can create an account. It gets the member role and everything that role
            grants.
          </span>
        </span>
      </Label>
      {error && (
        <p className="mt-2 text-sm text-destructive" role="alert">
          {error}
        </p>
      )}
      {open === undefined && <LoadError className="mt-2 mb-0" error={loadError} onRetry={reload} />}
    </div>
  )
}
