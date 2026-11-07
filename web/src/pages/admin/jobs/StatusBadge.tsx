import { Badge } from '../../../components/ui/display/badge'
import { cn } from '../../../lib/style/cn'
import type { Job } from '../../../types/repo/repo'

const statuses: Record<string, string> = { queued: '排隊中', running: '執行中', succeeded: '完成', failed: '失敗' }
const kinds: Record<string, string> = { index: '索引', wiki: 'Wiki' }

/** What a job status and kind read as, or the value itself for one this UI does not know; the values stay as the server sends them. */
export const statusLabel = (status: string) => statuses[status] ?? status
export const kindLabel = (kind: string) => kinds[kind] ?? kind

export function StatusBadge({ status }: { status: Job['status'] }) {
  return (
    <Badge
      variant="outline"
      className={cn(
        'text-[10.5px] tracking-[0.08em] uppercase',
        status === 'failed' && 'border-destructive/40 text-destructive',
      )}
    >
      {statusLabel(status)}
    </Badge>
  )
}
