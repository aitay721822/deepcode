import { Link } from '../../lib/router'
import { Notice } from '../layout/Notice'
import { Button } from '../ui/controls/button'
import { SkeletonText } from '../ui/feedback/skeleton'

/** In place of a page about a repository until it loads: loading, not found (or not the reader's), or failed, with Retry. */
export function RepoNotice({ error, status, onRetry }: { error?: string; status?: number; onRetry: () => void }) {
  if (!error) return <Notice><SkeletonText /></Notice>
  const missing = status === 404
  return (
    <Notice title={missing ? 'Repository not found' : 'Cannot open this repository'}>
      {missing ? (
        <p className="text-muted-foreground">It does not exist, or you do not have access to it.</p>
      ) : (
        <>
          <p className="[overflow-wrap:anywhere] text-destructive" role="alert">{error}</p>
          <Button variant="outline" onClick={onRetry}>Retry</Button>
        </>
      )}
      <Link to="/" className="mx-auto block w-fit text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">Back to repositories</Link>
    </Notice>
  )
}
