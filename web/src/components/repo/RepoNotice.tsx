import { Link } from '@tanstack/react-router'
import { Notice } from '../layout/Notice'
import { Button } from '../ui/controls/button'
import { SkeletonText } from '../ui/feedback/skeleton'

/** In place of a page about a repository until it loads: loading, not found (or not the reader's), or failed, with Retry. */
export function RepoNotice({ error, status, onRetry }: { error?: string; status?: number; onRetry: () => void }) {
  if (!error)
    return (
      <Notice>
        <SkeletonText />
      </Notice>
    )
  const missing = status === 404
  return (
    <Notice title={missing ? '找不到 Repository' : '無法開啟這個 Repository'}>
      {missing ? (
        <p className="text-muted-foreground">它不存在，或你沒有存取權限。</p>
      ) : (
        <>
          <p className="[overflow-wrap:anywhere] text-destructive" role="alert">
            {error}
          </p>
          <Button variant="outline" onClick={onRetry}>
            重試
          </Button>
        </>
      )}
      <Link
        to="/"
        className="mx-auto block w-fit text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
      >
        回到 Repository 列表
      </Link>
    </Notice>
  )
}
