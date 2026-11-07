import type { ComponentProps } from 'react'
import { cn } from '../../../lib/style/cn'

function Skeleton({ className, ...props }: ComponentProps<'div'>) {
  return (
    <div
      data-slot="skeleton"
      className={cn('animate-pulse rounded-md bg-accent forced-colors:border', className)}
      {...props}
    />
  )
}

/** Lines of placeholder text of uneven length, while something loads. */
function SkeletonText({ lines = 5, className }: { lines?: number; className?: string }) {
  return (
    <div role="status" aria-label="Loading" className={cn('grid gap-3 py-3', className)}>
      {Array.from({ length: lines }, (_, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: the placeholder lines are all alike; the index is all that tells them apart
        <Skeleton key={i} className="h-3" style={{ width: `${92 - ((i * 37) % 45)}%` }} />
      ))}
    </div>
  )
}

export { SkeletonText }
