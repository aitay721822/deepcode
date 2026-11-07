import { ChevronRightIcon } from 'lucide-react'
import { useLayoutEffect, useRef } from 'react'
import { Mermaid } from '../../../components/source/diagram/Mermaid'
import { Markdown } from '../../../components/source/Markdown'
import { SkeletonText } from '../../../components/ui/feedback/skeleton'
import { headingId } from '../../../lib/url'
import type { Repo } from '../../../types/repo/repo'
import type { WikiPage } from '../../../types/repo/wiki'
import type { TocItem } from './Toc'

/**
 * A wiki page; one without content is still being written (writing: a wiki job is queued or running),
 * or never was. Its sections are numbered (source.css, .wiki).
 */
export function WikiArticle({ page, repo, writing, onToc, onCite }: {
  page: WikiPage
  repo: Repo
  writing: boolean
  onToc: (t: TocItem[]) => void
  onCite: (path: string, start?: number, end?: number) => void
}) {
  const ref = useRef<HTMLElement>(null)
  // Headings get their ids from the rendered text, so the outline always matches.
  useLayoutEffect(() => {
    const used = new Set<string>()
    const items = [...(ref.current?.querySelectorAll<HTMLElement>('.markdown h2, .markdown h3') ?? [])].map((h) => {
      const text = h.textContent ?? ''
      // A repeated heading is numbered as on GitHub (overview, overview-1), so a link written for it reaches it.
      const base = headingId(text)
      let id = base
      for (let n = 1; used.has(id); n++) id = `${base}-${n}`
      used.add(id)
      h.id = id
      h.tabIndex = -1 // a link to it moves focus there, so Tab goes on from the section
      return { id, text, level: h.tagName === 'H3' ? 3 : 2 }
    })
    onToc(items)
  }, [page.content])
  const files = [...new Set(page.citations.map((c) => c.path))]
  return (
    <article ref={ref} className="wiki">
      <h1 className="mb-7 font-serif text-[clamp(2rem,3.6vw,2.75rem)] leading-[1.08] font-medium tracking-[-0.02em] [overflow-wrap:anywhere]">{page.title}</h1>
      {files.length > 0 && (
        <details className="group mb-9 rounded-lg border bg-card">
          <summary className="flex cursor-pointer list-none items-center gap-2.5 px-4 py-3 font-mono text-[11.5px] font-medium tracking-[0.12em] uppercase [&::-webkit-details-marker]:hidden">
            <ChevronRightIcon className="size-3.5 text-brand transition-transform group-open:rotate-90" /> Relevant source files
            <span className="ml-auto text-muted-foreground">{files.length}</span>
          </summary>
          <ul className="border-t border-dashed py-3 pr-4 pl-10 font-mono text-[12.5px] leading-[2]">
            {files.map((f) => (
              <li key={f}>
                <button className="text-left [overflow-wrap:anywhere] text-muted-foreground hover:text-brand hover:underline" onClick={() => onCite(f)}>{f}</button>
              </li>
            ))}
          </ul>
        </details>
      )}
      {page.diagram && (
        <figure className="mb-9">
          <Mermaid code={page.diagram} label="Imports between directories" />
          <figcaption className="-mt-2 text-center font-mono text-[12.5px] text-muted-foreground">Imports between directories, drawn from the code</figcaption>
        </figure>
      )}
      {page.content ? (
        <Markdown text={page.content} citations={page.citations} repo={repo} onCite={(c) => onCite(c.path, c.startLine, c.endLine)} />
      ) : writing ? (
        <SkeletonText lines={8} />
      ) : (
        <p className="font-serif text-[17px] text-muted-foreground italic">This page was not written.</p>
      )}
    </article>
  )
}
