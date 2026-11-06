import { motion } from 'motion/react'
import { Spinner } from '../../../components/ui/feedback/spinner'
import { Link } from '../../../lib/router'
import { cn } from '../../../lib/style/cn'
import { markerRadius, slide } from '../../../lib/style/motion'
import type { WikiPage } from '../../../types/repo/wiki'

/** The wiki's pages, numbered; the current one's marker glides to the next (motion layoutId). writing: empty pages are still to come. */
export function WikiNav({ pages, active, base, writing }: { pages: WikiPage[]; active?: string; base: string; writing: boolean }) {
  return (
    <nav className="isolate flex flex-col border-l" aria-label="Wiki pages">
      {pages.map((p, i) => {
        const current = p.slug === active
        return (
          <Link key={p.slug} to={`${base}?page=${encodeURIComponent(p.slug)}`} aria-current={current ? 'page' : undefined}
            className={cn('relative -ml-px flex items-baseline gap-3 py-[7px] pr-2 pl-4 text-sm leading-snug text-muted-foreground transition-colors hover:text-foreground', current && 'text-foreground')}>
            {current && <motion.span layoutId="wiki-nav" className="absolute inset-0 -z-10 border-l-2 border-brand bg-linear-to-r from-brand/12 to-transparent" transition={slide}
              style={{ borderTopRightRadius: markerRadius, borderBottomRightRadius: markerRadius }} />}
            <span aria-hidden="true" className={cn('flex-none font-mono text-[10.5px] tabular-nums', current ? 'text-brand' : 'text-faint')}>{String(i + 1).padStart(2, '0')}</span>
            <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">{p.title}</span>
            {!p.content && writing && <Spinner className="size-3 flex-none self-center" role="img" aria-label="Writing" />}
          </Link>
        )
      })}
    </nav>
  )
}
