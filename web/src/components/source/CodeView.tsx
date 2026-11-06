import { useLayoutEffect, useMemo, useRef } from 'react'
import { highlight } from '../../lib/style/highlight'
import { cn } from '../../lib/style/cn'
import './source.css'

/**
 * The whole file is highlighted as one block (so multi-line strings and
 * comments stay right); line numbers and the cited range are separate layers
 * on the same line grid. The band is measured in lines (lh), so it stays on
 * its lines when a reader's own text spacing (WCAG 1.4.12) changes their height.
 * Paper cannot scroll, so there the code wraps, without the line numbers and band
 * that wrapped lines would no longer meet.
 */
export function CodeView({ label, content, language, startLine, endLine, className }: { label: string; content: string; language: string; startLine?: number; endLine?: number; className?: string }) {
  const code = content.replace(/\n$/, '')
  const html = useMemo(() => highlight(code, language), [code, language])
  const count = code.split('\n').length
  const ref = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    const box = ref.current
    if (box) box.scrollTop = startLine ? Math.max(0, (startLine - 4) * parseFloat(getComputedStyle(box).lineHeight)) : 0
  }, [startLine, code])
  const end = Math.min(endLine ?? startLine ?? 0, count)
  // Line numbers as three blocks of text (before, cited, after), not an element per line.
  const numbers = Array.from({ length: count }, (_, i) => i + 1)
  const from = startLine ? Math.min(startLine, count + 1) : count + 1
  const to = Math.max(end, from - 1)
  const gutter = [numbers.slice(0, from - 1), numbers.slice(from - 1, to), numbers.slice(to)]
  return (
    <div ref={ref} tabIndex={0} role="region" aria-label={label} className={cn('flex-1 overflow-auto overscroll-contain font-mono text-[13px] leading-5 focus-visible:outline-offset-[-2px] print:overflow-visible', className)}>
      <div className="relative flex min-w-max py-2 print:min-w-0">
        {startLine ? <div className="absolute inset-x-0 mt-2 bg-(--band) forced-colors:border-y print:hidden" style={{ top: `${startLine - 1}lh`, height: `${end - startLine + 1}lh` }} /> : null}
        <div className="relative pr-3.5 pl-3 text-right whitespace-pre text-faint select-none print:hidden" aria-hidden="true">
          {gutter.map((part, i) => part.length > 0 && <span key={i} className={cn('block', i === 1 && 'text-foreground')}>{part.join('\n')}</span>)}
        </div>
        <pre className="relative pr-6 [tab-size:4] print:pl-3 print:whitespace-pre-wrap print:[overflow-wrap:anywhere]"><code className="hljs" dangerouslySetInnerHTML={{ __html: html }} /></pre>
      </div>
    </div>
  )
}
