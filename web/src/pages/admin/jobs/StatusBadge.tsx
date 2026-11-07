import { Badge } from '../../../components/ui/display/badge'
import { cn } from '../../../lib/style/cn'
import type { Job } from '../../../types/repo/repo'

export function StatusBadge({ status }: { status: Job['status'] }) {
  return (
    <Badge
      variant="outline"
      className={cn(
        'text-[10.5px] tracking-[0.08em] uppercase',
        status === 'failed' && 'border-destructive/40 text-destructive',
      )}
    >
      {status}
    </Badge>
  )
}
