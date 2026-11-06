import { useEffect, useState } from 'react'
import { getSignUp, login } from '../../api/auth'
import { Button } from '../../components/ui/controls/button'
import { Spinner } from '../../components/ui/feedback/spinner'
import { Field } from '../../components/ui/form/field'
import { Input } from '../../components/ui/form/input'
import { Link, navigate } from '../../lib/router'
import { useApi } from '../../lib/useApi'
import { destination, useSession, withNext } from './session'
import { SessionCard, SessionError } from './SessionCard'

export function LoginPage({ next }: { next: string | null }) {
  const { user, refresh } = useSession()
  const { data: signUpState } = useApi('signup', getSignUp) // sign-up is offered only while open
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (user) navigate(destination(next), { replace: true }) // signed in, now or already
  }, [user])

  return (
    <SessionCard title="Sign in" heading={<>Sign in to <em>deepcode</em></>}>
      <form className="grid gap-4" aria-labelledby="session-title" onSubmit={async (e) => {
        e.preventDefault()
        setBusy(true)
        setError('')
        try {
          await login(username, password)
          await refresh()
        } catch (err) {
          setError((err as Error).message) // 401: wrong credentials, 429: too many attempts; or the session failed to load
        } finally {
          setBusy(false)
        }
      }}>
        <Field label="Username">
          <Input autoComplete="username" autoFocus required value={username} onChange={(e) => setUsername(e.target.value)} />
        </Field>
        <Field label="Password">
          <Input type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
        </Field>
        <SessionError error={error} />
        <Button type="submit" size="lg" className="mt-1 w-full" busy={busy}>
          {busy && <Spinner />} Sign in
        </Button>
      </form>
      {signUpState?.open && (
        <p className="text-center text-sm text-muted-foreground">
          No account yet? <Link to={withNext('/signup', next)} className="text-brand underline-offset-4 hover:underline">Create one</Link>
        </p>
      )}
    </SessionCard>
  )
}
