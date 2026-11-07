import { Link, useNavigate } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { getSignUp, login } from '../../api/auth'
import { Button } from '../../components/ui/controls/button'
import { Spinner } from '../../components/ui/feedback/spinner'
import { Field } from '../../components/ui/form/field'
import { Input } from '../../components/ui/form/input'
import { useApi } from '../../lib/useApi'
import { SessionCard, SessionError } from './SessionCard'
import { destination, useSession } from './session'

export function LoginPage({ next }: { next?: string }) {
  const navigate = useNavigate()
  const { user, refresh } = useSession()
  const { data: signUpState } = useApi('signup', getSignUp) // sign-up is offered only while open
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (user) navigate({ href: destination(next), replace: true, resetScroll: false }) // signed in, now or already
  }, [user, next, navigate])

  return (
    <SessionCard
      title="登入"
      heading={
        <>
          登入 <em>deepcode</em>
        </>
      }
    >
      <form
        className="grid gap-4"
        aria-labelledby="session-title"
        onSubmit={async (e) => {
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
        }}
      >
        <Field label="使用者名稱">
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
        <Field label="密碼">
          <Input
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </Field>
        <SessionError error={error} />
        <Button type="submit" size="lg" className="mt-1 w-full" busy={busy}>
          {busy && <Spinner />} 登入
        </Button>
      </form>
      {signUpState?.open && (
        <p className="text-center text-sm text-muted-foreground">
          還沒有帳號？
          <Link to="/signup" search={{ next }} className="text-brand underline-offset-4 hover:underline">
            建立帳號
          </Link>
        </p>
      )}
    </SessionCard>
  )
}
