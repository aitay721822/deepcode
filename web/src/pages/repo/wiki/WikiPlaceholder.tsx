import { RefreshCwIcon } from 'lucide-react'
import { Notice } from '../../../components/layout/Notice'
import { isActive } from '../../../components/repo/jobStage'
import { Button } from '../../../components/ui/controls/button'
import { SkeletonText } from '../../../components/ui/feedback/skeleton'
import { Spinner } from '../../../components/ui/feedback/spinner'
import { Status } from '../../../components/ui/feedback/status'
import type { Health, Repo } from '../../../types/repo/repo'
import type { Wiki } from '../../../types/repo/wiki'
import type { Sync } from '../useSync'

/**
 * In place of a wiki without pages (the wiki job's own card stands in while it runs or once it failed):
 * why the wiki did not load (onReload retries), or that there is none (sync: given when the reader may sync).
 * Loading, health included, shows a skeleton.
 */
export function WikiPlaceholder({
  repo,
  health,
  wiki,
  error,
  onReload,
  sync,
}: {
  repo: Repo
  health?: Health | null // null: it failed to load, so whether a chat model is set is unknown
  wiki?: Wiki
  error?: string
  onReload: () => void
  sync?: Sync
}) {
  if (error) {
    return (
      <Notice>
        <p className="text-sm text-destructive" role="alert">
          {error}
        </p>
        <Button variant="outline" onClick={onReload}>
          重試
        </Button>
      </Notice>
    )
  }
  if (!wiki || health === undefined)
    return (
      <Notice>
        <SkeletonText lines={6} />
      </Notice>
    )
  if (health && !health.chatModel) {
    return (
      <Notice>
        <p className="text-muted-foreground">
          請先設定 DEEPCODE_CHAT_MODEL 才能產生 Wiki。檔案與 Symbol 搜尋已可使用。
        </p>
      </Notice>
    )
  }
  const indexing = isActive(repo.job) // an index job: a wiki is written after it
  return (
    <Notice title="還沒有 Wiki">
      <p className="text-muted-foreground">{indexing && '索引建立完成後就會產生。'}檔案與 Symbol 搜尋已可使用。</p>
      {sync && !indexing && (
        <>
          <Button variant="outline" busy={sync.busy} onClick={sync.run}>
            {sync.busy ? <Spinner /> : <RefreshCwIcon />} 同步
          </Button>
          {sync.error && (
            <p className="text-sm text-destructive" role="alert">
              {sync.error}
            </p>
          )}
          {sync.upToDate && <Status className="text-sm text-muted-foreground">已是最新的 Commit</Status>}
        </>
      )}
    </Notice>
  )
}
