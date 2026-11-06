import { cn } from '../../../lib/style/cn'

export type TocItem = { id: string; text: string; level: number }

/** The page's outline; sections carry the same numbers as their headings (source.css, .wiki). */
export function Toc({ items }: { items: TocItem[] }) {
  let section = 0
  const numbered = items.map((i) => ({ ...i, n: i.level === 2 ? ++section : 0 }))
  return (
    <nav aria-label="On this page">
      <div className="eyebrow mb-3 text-muted-foreground">On this page</div>
      <ul className="border-l">
        {numbered.map((i) => (
          <li key={i.id}>
            <a href={`#${i.id}`}
              className={cn('-ml-px flex gap-2.5 border-l border-transparent py-[5px] pl-3.5 text-[13px] leading-snug [overflow-wrap:anywhere] text-muted-foreground transition-colors hover:border-brand hover:text-foreground', i.level === 3 && 'pl-7')}
              onClick={(e) => {
                if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return // a new tab or window: the browser's own way
                e.preventDefault()
                const still = matchMedia('(prefers-reduced-motion: reduce)').matches
                const heading = document.getElementById(i.id)
                heading?.scrollIntoView({ behavior: still ? 'auto' : 'smooth', block: 'start' })
                heading?.focus({ preventScroll: true }) // as a plain #link does: Tab goes on from the section
              }}>
              {i.n > 0 && <span aria-hidden="true" className="flex-none pt-px font-mono text-[10px] text-faint">{String(i.n).padStart(2, '0')}</span>}
              {i.text}
            </a>
          </li>
        ))}
      </ul>
    </nav>
  )
}
