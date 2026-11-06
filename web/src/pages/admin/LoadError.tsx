import { useRef } from 'react'
import { Button } from '../../components/ui/controls/button'
import { cn } from '../../lib/style/cn'

/**
 * Why a section's lists did not load, and a Retry that loads them again. The button goes away while they load,
 * so it first hands focus to the console's main region rather than letting it drop to the page.
 */
export function LoadError({ error, onRetry, className }: { error?: string; onRetry: () => void; className?: string }) {
  const ref = useRef<HTMLDivElement>(null)
  if (!error) return null
  const retry = () => {
    ref.current?.closest('main')?.focus({ preventScroll: true })
    onRetry()
  }
  return (
    <div ref={ref} className={cn('mb-3 flex flex-wrap items-center gap-3', className)}>
      <p className="text-sm [overflow-wrap:anywhere] text-destructive" role="alert">{error}</p>
      <Button variant="outline" size="sm" onClick={retry}>Retry</Button>
    </div>
  )
}
