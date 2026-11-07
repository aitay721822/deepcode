import { type ReactNode, useRef } from 'react'
import { cn } from '../../../lib/style/cn'
import { Button } from '../controls/button'

/**
 * Why something did not load, a Retry that loads it again, and children after them (a link to the provider, say).
 * The button goes away while it loads, so it first hands focus to the nearest region that takes it (a page's main
 * region, the file panel's tab panel) rather than letting it drop to the page.
 */
export function LoadError({
  error,
  onRetry,
  className,
  children,
}: {
  error?: string
  onRetry: () => void
  className?: string
  children?: ReactNode
}) {
  const ref = useRef<HTMLDivElement>(null)
  if (!error) return null
  const retry = () => {
    ref.current?.closest<HTMLElement>('[tabindex]')?.focus({ preventScroll: true })
    onRetry()
  }
  return (
    <div ref={ref} className={cn('mb-3 flex flex-wrap items-center gap-3', className)}>
      <p className="text-sm [overflow-wrap:anywhere] text-destructive" role="alert">
        {error}
      </p>
      <Button variant="outline" size="sm" onClick={retry}>
        Retry
      </Button>
      {children}
    </div>
  )
}
