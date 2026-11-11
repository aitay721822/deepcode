import { CopyIcon } from 'lucide-react'
import { useEffect, useState } from 'react'
import { clearWebhook, getWebhook, setWebhook } from '../../api/repo/repos'
import { Button } from '../../components/ui/controls/button'
import { Card } from '../../components/ui/display/card'
import { Spinner } from '../../components/ui/feedback/spinner'
import type { Repo } from '../../types/repo/repo'

/** The webhook card: its endpoint, a secret shown once, and how to turn it off. */
export function WebhookCard({ repo }: { repo: Repo }) {
  const [hasSecret, setHasSecret] = useState(false)
  const [secret, setSecret] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const endpoint = `${window.location.origin}/api/hooks/${repo.id}`

  useEffect(() => {
    let live = true
    getWebhook(repo.id)
      .then((r) => live && setHasSecret(r.hasSecret))
      .catch((e) => live && setError(e instanceof Error ? e.message : String(e)))
    return () => {
      live = false
    }
  }, [repo.id])

  async function generate() {
    setBusy(true)
    setError('')
    try {
      const r = await setWebhook(repo.id)
      setSecret(r.secret)
      setHasSecret(true)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function clear() {
    setBusy(true)
    setError('')
    try {
      await clearWebhook(repo.id)
      setHasSecret(false)
      setSecret('')
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card className="gap-0 p-4">
      <p className="font-mono text-[12px] text-muted-foreground">Webhook</p>
      <p className="mt-1 text-[12px] [overflow-wrap:anywhere] text-muted-foreground">
        POST <span className="font-mono text-foreground">{endpoint}</span>
      </p>
      <div className="mt-3 flex flex-wrap items-center gap-2">
        <Button variant="outline" size="sm" busy={busy} onClick={generate}>
          {busy ? <Spinner /> : null} {hasSecret ? '重新產生密鑰' : '產生密鑰'}
        </Button>
        {hasSecret && (
          <Button variant="ghost" size="sm" disabled={busy} onClick={clear}>
            移除
          </Button>
        )}
      </div>
      {secret && (
        <div className="mt-3">
          <p className="text-[12px] text-muted-foreground">密鑰只顯示一次，請立即保存</p>
          <div className="mt-1 flex items-center gap-2">
            <input
              readOnly
              aria-label="Webhook 密鑰"
              value={secret}
              className="h-9 min-w-0 flex-1 rounded-md border border-input bg-card px-3 font-mono text-[12px]"
            />
            <Button
              variant="outline"
              size="sm"
              onClick={() => navigator.clipboard.writeText(secret)}
              aria-label="複製密鑰"
            >
              <CopyIcon /> 複製
            </Button>
          </div>
        </div>
      )}
      {error && (
        <p className="mt-2 text-[13px] [overflow-wrap:anywhere] text-destructive" role="alert">
          {error}
        </p>
      )}
    </Card>
  )
}
