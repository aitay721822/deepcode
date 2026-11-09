import { useEffect, useRef, useState } from 'react'
import { answerAuth, getAuthApp } from '../../../api/auth'
import { Button } from '../../../components/ui/controls/button'
import { LoadError } from '../../../components/ui/feedback/load-error'
import { SkeletonText } from '../../../components/ui/feedback/skeleton'
import { Spinner } from '../../../components/ui/feedback/spinner'
import { useApi } from '../../../lib/useApi'
import type { AuthRequest } from '../../../types/auth'
import { SessionCard, SessionError } from '../SessionCard'
import { useSession } from '../session'

/**
 * /oauth/authorize: an app asks to act for the signed-in user. Named by what it registered, and by where it sends
 * the browser back to, so a look-alike name shows its own address; approving or declining sends the browser there.
 */
export function AuthorizePage({ request }: { request: AuthRequest }) {
  const { user } = useSession()
  const {
    data: app,
    error,
    reload,
  } = useApi(request.clientId && request.redirectUri ? `oauth:${request.clientId}:${request.redirectUri}` : null, () =>
    getAuthApp(request.clientId, request.redirectUri),
  )
  const [busy, setBusy] = useState<'' | 'approve' | 'decline'>('')
  // refused: approved, but the request itself was wrong (no PKCE, say), which the app is told instead of a code.
  const [sent, setSent] = useState<'' | 'approved' | 'declined' | 'refused'>('')
  const status = useRef<HTMLParagraphElement>(null)
  useEffect(() => {
    if (sent) status.current?.focus()
  }, [sent])
  const [failed, setFailed] = useState('')
  const answer = async (approve: boolean) => {
    setBusy(approve ? 'approve' : 'decline')
    setFailed('')
    try {
      const { redirect } = await answerAuth(request, approve)
      location.assign(redirect) // the app's address, which it registered
      // An app's own scheme (cursor://…) opens the app and leaves this page here: say what happened.
      const refused = approve && new URL(redirect).searchParams.has('error')
      setSent(refused ? 'refused' : approve ? 'approved' : 'declined')
    } catch (e) {
      setFailed((e as Error).message)
    }
    setBusy('')
  }
  let back = request.redirectUri
  try {
    const u = new URL(request.redirectUri)
    back =
      u.protocol === 'http:' || u.protocol === 'https:' ? u.origin : u.host ? `${u.protocol}//${u.host}` : u.protocol
  } catch {
    // shown as sent
  }
  return (
    <SessionCard
      title="授權應用程式"
      heading={
        app ? (
          <>
            授權{' '}
            <em dir="auto" className="[overflow-wrap:anywhere]">
              {app.name}
            </em>
          </>
        ) : (
          '授權應用程式'
        )
      }
    >
      {!request.clientId || !request.redirectUri ? (
        <p className="text-sm text-destructive" role="alert">
          這個授權連結缺少應用程式或重新導向網址：請在應用程式裡重新連線。
        </p>
      ) : error ? (
        <LoadError className="m-0" error={error} onRetry={reload} />
      ) : !app ? (
        <SkeletonText />
      ) : (
        <>
          <p className="text-[15px] leading-relaxed">
            <strong dir="auto" className="[overflow-wrap:anywhere]">
              {app.name}
            </strong>{' '}
            想以 <strong>{user?.displayName || user?.username}</strong> 的身分使用 deepcode：讀取你可讀的
            Repository、Wiki 與程式碼，並以你的名義提問。
          </p>
          <p className="text-sm text-muted-foreground">
            授權後會回到 <code className="font-mono [overflow-wrap:anywhere]">{back}</code>
            。只授權你自己剛剛從應用程式發起的連線；之後可在「存取權杖與 MCP」撤銷。
          </p>
          <SessionError error={failed} />
          {sent ? (
            // Focused, as the button pressed is gone: a screen reader reads where the answer went.
            <p ref={status} tabIndex={-1} role="status" className="text-sm font-medium outline-none">
              {
                {
                  approved: '已授權。',
                  declined: '已拒絕。',
                  refused: '這個授權請求無效（例如缺少 PKCE），原因已送回應用程式。',
                }[sent]
              }
              若瀏覽器沒有自動切回應用程式，請直接回到應用程式；這個分頁可以關閉。
            </p>
          ) : (
            <div className="grid grid-cols-2 gap-3">
              <Button
                variant="outline"
                size="lg"
                disabled={!!busy}
                busy={busy === 'decline'}
                onClick={() => answer(false)}
              >
                {busy === 'decline' && <Spinner />} 拒絕
              </Button>
              <Button size="lg" disabled={!!busy} busy={busy === 'approve'} onClick={() => answer(true)}>
                {busy === 'approve' && <Spinner />} 授權
              </Button>
            </div>
          )}
        </>
      )}
    </SessionCard>
  )
}
