import { Link } from '@tanstack/react-router'
import { motion } from 'motion/react'
import { Spinner } from '../../../components/ui/feedback/spinner'
import { cn } from '../../../lib/style/cn'
import { markerRadius, slide } from '../../../lib/style/motion'
import type { WikiPage } from '../../../types/repo/wiki'

/** Each page's number: 1, 2, … at the top, and 2.1, 2.2, … for the pages under the section page 2. */
function numbers(pages: WikiPage[]): string[] {
  const of = new Map<string, string>() // by slug
  const under = new Map<string, number>() // pages numbered so far under each section, by its slug
  let top = 0
  return pages.map((p) => {
    const section = p.parent ? of.get(p.parent) : undefined
    let n = String(++top)
    if (p.parent && section) {
      top--
      const k = (under.get(p.parent) ?? 0) + 1
      under.set(p.parent, k)
      n = `${section}.${k}`
    }
    of.set(p.slug, n)
    return n
  })
}

/**
 * The wiki's pages, numbered, a section's pages indented under it; the current one's marker glides to the next (motion
 * layoutId). writing: empty pages are still to come.
 */
export function WikiNav({
  pages,
  active,
  repoId,
  writing,
}: {
  pages: WikiPage[]
  active?: string
  repoId: string
  writing: boolean
}) {
  const numbered = numbers(pages)
  return (
    <nav className="isolate flex flex-col gap-0.5" aria-label="Wiki 頁面">
      {pages.map((p, i) => {
        const current = p.slug === active
        const part = numbered[i].includes('.')
        return (
          <Link
            key={p.slug}
            to="/repos/$id"
            params={{ id: repoId }}
            search={{ page: p.slug }}
            aria-current={current ? 'page' : undefined}
            className={cn(
              'relative flex items-baseline gap-3 rounded-sm py-[7px] pr-2 pl-3 text-sm leading-snug text-muted-foreground transition-colors hover:text-foreground',
              part && 'ml-5 py-[5px] text-[13px]',
              current && 'font-medium text-foreground',
            )}
          >
            {current && (
              <motion.span
                layoutId="wiki-nav"
                className="absolute inset-0 -z-10 bg-accent forced-colors:border"
                transition={slide}
                style={{ borderRadius: markerRadius }}
              />
            )}
            <span
              aria-hidden="true"
              className={cn(
                'min-w-7 flex-none font-mono text-[10.5px] tabular-nums',
                current ? 'text-brand' : 'text-faint',
              )}
            >
              {numbered[i]}
            </span>
            <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">{p.title}</span>
            {!p.content && writing && (
              <Spinner className="size-3 flex-none self-center" role="img" aria-label="撰寫中" />
            )}
          </Link>
        )
      })}
    </nav>
  )
}
