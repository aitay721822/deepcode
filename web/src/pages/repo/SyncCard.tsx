import { RefreshCwIcon, RotateCcwIcon, SquareIcon } from 'lucide-react'
import { rebuildRepo } from '../../api/repo/repos'
import { stageCount, stageName } from '../../components/repo/jobStage'
import { Button } from '../../components/ui/controls/button'
import { Card } from '../../components/ui/display/card'
import { Spinner } from '../../components/ui/feedback/spinner'
import { ConfirmDialog } from '../../components/ui/overlay/confirm-dialog'
import { shortSha } from '../../lib/format'
import { cn } from '../../lib/style/cn'
import { useDialog } from '../../lib/useDialog'
import type { Repo } from '../../types/repo/repo'
import { useStop, useSync } from './useSync'

export function SyncCard({ repo, jobCard, onSynced }: { repo: Repo; jobCard: boolean; onSynced: () => void }) {
  const { busy, error, upToDate, run } = useSync(repo.id, onSynced)
  const job = repo.job
  const stop = useStop(job, onSynced)
  const active = stop.active
  const rebuild = useDialog<true>()
  // jobCard: the page shows the job in a card of its own, which already says why it failed.
  const failed = job?.status === 'failed' && !jobCard ? job.error : ''
  const note =
    job && active
      ? stageName(job)
      : upToDate
        ? '已是最新的 Commit'
        : failed || (repo.snapshot ? `目前為 ${shortSha(repo.snapshot.commitSha)}` : '')
  const count = job && active ? stageCount(job) : ''
  return (
    <Card className="gap-0 p-4">
      {/* A live region from the start, so what Sync leads to is read out. The count stays outside it, or every poll would be. */}
      <p className={cn('font-mono text-[12px] [overflow-wrap:anywhere] text-muted-foreground', note && 'mb-3')}>
        <span role="status">{note}</span>
        {count && ` ${count}`}
      </p>
      {(error || stop.error) && (
        <p className="mb-3 text-[13px] [overflow-wrap:anywhere] text-destructive" role="alert">
          {error || stop.error}
        </p>
      )}
      <div className="flex gap-2">
        <Button variant="outline" size="sm" busy={busy || active} onClick={run}>
          {active || busy ? <Spinner /> : <RefreshCwIcon />} 同步
        </Button>
        {active ? (
          <Button variant="outline" size="sm" busy={stop.asked} onClick={stop.run}>
            {stop.asked ? <Spinner /> : <SquareIcon />} 停止
          </Button>
        ) : (
          <Button variant="outline" size="sm" disabled={busy} onClick={() => rebuild.show(true)}>
            <RotateCcwIcon /> 重新索引
          </Button>
        )}
      </div>
      <ConfirmDialog
        key={rebuild.key}
        open={rebuild.open}
        title="重新索引？"
        confirm="重新索引"
        description="每個檔案都會重新解析，整份 Wiki 重新規劃、每頁重寫，會用到較多 LLM 用量。重寫期間仍可閱讀現有的 Wiki 或已寫好的新頁面。「同步」只處理有變動的部分。"
        onClose={rebuild.hide}
        onConfirm={async () => {
          await rebuildRepo(repo.id)
          onSynced()
        }}
      />
    </Card>
  )
}
