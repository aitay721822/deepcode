import { Link, useNavigate } from '@tanstack/react-router'
import { type ReactNode, useEffect, useState } from 'react'
import { getSignUp, signUp } from '../../api/auth'
import { Button } from '../../components/ui/controls/button'
import { SkeletonText } from '../../components/ui/feedback/skeleton'
import { Spinner } from '../../components/ui/feedback/spinner'
import { Field } from '../../components/ui/form/field'
import { Input } from '../../components/ui/form/input'
import { useApi } from '../../lib/useApi'
import { SessionCard, SessionError } from './SessionCard'
import { destination, useSession } from './session'

/** Creating your own account while an administrator has opened sign-up: it gets the member role and is signed in at once. */
export function SignUpPage({ next }: { next?: string }) {
  const navigate = useNavigate()
  const { user, refresh } = useSession()
  const { data: state, error: stateError } = useApi('signup', getSignUp)
  const [username, setUsername] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (user) navigate({ href: destination(next), replace: true, resetScroll: false }) // signed up, or already signed in
  }, [user, next, navigate])

  let body: ReactNode
  if (!state && !stateError)
    body = <SkeletonText lines={4} /> // not a form that may turn out closed
  else if (state?.open === false)
    body = <p className="text-center text-sm text-muted-foreground">目前不開放註冊。請向管理員申請帳號。</p>
  else
    body = // open, or unknown: the server still refuses a closed sign-up
      (
        <form
          className="grid gap-4"
          aria-labelledby="session-title"
          onSubmit={async (e) => {
            e.preventDefault()
            if (password !== confirm) {
              setError('兩次輸入的密碼不一致。')
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
          <Field label="使用者名稱" hint="2 到 32 個英文字母、數字、「.」、「_」或「-」，須以英文字母或數字開頭">
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
          <Field label="顯示名稱" hint="選填，最多 64 個字元；未填則使用使用者名稱">
            <Input autoComplete="name" value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
          </Field>
          <Field label="密碼" hint="8 到 256 個字元">
            <Input
              type="password"
              autoComplete="new-password"
              required
              minLength={8}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>
          <Field label="確認密碼">
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
            {busy && <Spinner />} 建立帳號
          </Button>
        </form>
      )
  return (
    <SessionCard
      title="建立帳號"
      heading={
        <>
          加入 <em>deepcode</em>
        </>
      }
    >
      {body}
      <p className="text-center text-sm text-muted-foreground">
        已經有帳號？
        <Link to="/login" search={{ next }} className="text-brand underline underline-offset-4">
          登入
        </Link>
      </p>
    </SessionCard>
  )
}
