import { type ReactNode, useEffect, useState } from 'react'
import { getSignUp, signUp } from '../../api/auth'
import { Button } from '../../components/ui/controls/button'
import { SkeletonText } from '../../components/ui/feedback/skeleton'
import { Spinner } from '../../components/ui/feedback/spinner'
import { Field } from '../../components/ui/form/field'
import { Input } from '../../components/ui/form/input'
import { Link, navigate } from '../../lib/router'
import { useApi } from '../../lib/useApi'
import { SessionCard, SessionError } from './SessionCard'
import { destination, useSession, withNext } from './session'

/** Creating your own account while an administrator has opened sign-up: it gets the member role and is signed in at once. */
export function SignUpPage({ next }: { next: string | null }) {
  const { user, refresh } = useSession()
  const { data: state, error: stateError } = useApi('signup', getSignUp)
  const [username, setUsername] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (user) navigate(destination(next), { replace: true }) // signed up, or already signed in
  }, [user, next])

  let body: ReactNode
  if (!state && !stateError)
    body = <SkeletonText lines={4} /> // not a form that may turn out closed
  else if (state?.open === false)
    body = (
      <p className="text-center text-sm text-muted-foreground">
        Sign-up is closed. Ask an administrator for an account.
      </p>
    )
  else
    body = // open, or unknown: the server still refuses a closed sign-up
      (
        <form
          className="grid gap-4"
          aria-labelledby="session-title"
          onSubmit={async (e) => {
            e.preventDefault()
            if (password !== confirm) {
              setError('The passwords do not match.')
              return
            }
            setBusy(true)
            setError('')
            try {
              await signUp(username, displayName, password)
              await refresh()
            } catch (err) {
              setError((err as Error).message) // 400 invalid, 403 closed meanwhile, 409 name taken, 429 too many sign-ups
            } finally {
              setBusy(false)
            }
          }}
        >
          <Field label="Username" hint="2 to 32 letters, digits, '.', '_' or '-', starting with a letter or digit">
            <Input
              autoComplete="username"
              autoCapitalize="none"
              spellCheck={false}
              autoFocus
              required
              value={username}
              onChange={(e) => setUsername(e.target.value)}
            />
          </Field>
          <Field label="Display name" hint="Optional, up to 64 characters; your username otherwise">
            <Input autoComplete="name" value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
          </Field>
          <Field label="Password" hint="8 to 256 characters">
            <Input
              type="password"
              autoComplete="new-password"
              required
              minLength={8}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>
          <Field label="Confirm password">
            <Input
              type="password"
              autoComplete="new-password"
              required
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
            />
          </Field>
          <SessionError error={error} />
          <Button type="submit" size="lg" className="mt-1 w-full" busy={busy}>
            {busy && <Spinner />} Create account
          </Button>
        </form>
      )
  return (
    <SessionCard
      title="Create account"
      heading={
        <>
          Join <em>deepcode</em>
        </>
      }
    >
      {body}
      <p className="text-center text-sm text-muted-foreground">
        Have an account?{' '}
        <Link to={withNext('/login', next)} className="text-brand underline-offset-4 hover:underline">
          Sign in
        </Link>
      </p>
    </SessionCard>
  )
}
