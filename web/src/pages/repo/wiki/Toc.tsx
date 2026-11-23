import { Link, useNavigate } from '@tanstack/react-router'
import { cn } from '../../../lib/style/cn'

export type TocItem = { id: string; text: string; level: number }

/**
 * The page's outline; sections carry the same numbers as their headings (source.css, .wiki).
 * Level 3 lines up with the level-2 titles: past the number column (w-3) and the gap (2.5).
 */
export function Toc({ items }: { items: TocItem[] }) {
  const navigate = useNavigate()
  let section = 0
  const numbered = items.map((i) => ({ ...i, n: i.level === 2 ? ++section : 0 }))
  return (
    <nav aria-label="本頁內容">
      <div className="eyebrow mb-3 text-muted-foreground">本頁內容</div>
      <ul>
        {numbered.map((i) => {
          const destination = {
            to: '.',
            hash: i.id,
            search: true,
            resetScroll: false,
            hashScrollIntoView: false,
          } as const
          return (
            <li key={i.id}>
              <Link
                {...destination}
                activeOptions={{ includeHash: true }}
                className={cn(
                  'flex gap-2.5 py-1.5 text-[13px] leading-[1.45] [overflow-wrap:anywhere] text-muted-foreground transition-colors hover:text-foreground',
                  i.level === 3 && 'pl-[22px]',
                )}
                onClick={async (e) => {
                  if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return // a new tab or window: the browser's own way
                  e.preventDefault()
                  await navigate(destination)
                  const still = matchMedia('(prefers-reduced-motion: reduce)').matches
                  const heading = document.getElementById(i.id)
                  heading?.scrollIntoView({ behavior: still ? 'auto' : 'smooth', block: 'start' })
                  heading?.focus({ preventScroll: true }) // as a plain #link does: Tab goes on from the section
                }}
              >
                {i.n > 0 && (
                  <span aria-hidden="true" className="w-3 flex-none pt-px font-mono text-[10px] text-faint">
                    {String(i.n).padStart(2, '0')}
                  </span>
                )}
                {i.text}
              </Link>
            </li>
          )
        })}
      </ul>
    </nav>
  )
}
