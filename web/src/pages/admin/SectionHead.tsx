import type { ReactNode } from 'react'
import { cn } from '../../lib/style/cn'

/** An admin section's title, with its actions beside it. */
export function SectionHead({ title, className, children }: { title: ReactNode; className?: string; children?: ReactNode }) {
  return (
    <div className={cn('mb-6 flex flex-wrap items-center justify-between gap-3', className)}>
      <h1 className="min-w-0 font-serif text-[30px] leading-[1.1] font-normal tracking-[-0.02em] [overflow-wrap:anywhere]">{title}</h1>
      {children}
    </div>
  )
}
