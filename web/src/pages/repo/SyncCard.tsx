import { RefreshCwIcon } from 'lucide-react'
import { isActive, stageCount, stageName } from '../../components/repo/jobStage'
import { Button } from '../../components/ui/controls/button'
import { Card } from '../../components/ui/display/card'
import { Spinner } from '../../components/ui/feedback/spinner'
import { shortSha } from '../../lib/format'
import { cn } from '../../lib/style/cn'
import type { Repo } from '../../types/repo/repo'
import { useSync } from './useSync'

export function SyncCard({ repo, jobCard, onSynced }: { repo: Repo; jobCard: boolean; onSynced: () => void }) {
  const { busy, error, upToDate, run } = useSync(repo.id, onSynced)
  const job = repo.job
  const active = isActive(job)
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
      {error && (
        <p className="mb-3 text-[13px] [overflow-wrap:anywhere] text-destructive" role="alert">
          {error}
        </p>
      )}
      <Button variant="outline" size="sm" className="w-fit" busy={busy || active} onClick={run}>
        {active || busy ? <Spinner /> : <RefreshCwIcon />} 同步
      </Button>
    </Card>
  )
}
