import { motion } from 'motion/react'
import { useLayoutEffect, useRef } from 'react'
import { formatTime } from '../../../lib/format'
import type { LogLine } from '../../../types/admin/jobs'

const clock: Intl.DateTimeFormatOptions = { hour: '2-digit', minute: '2-digit', second: '2-digit' }

/** Output lines; those after liveAfter arrived live and fade in. Follows the end unless the reader scrolled up. */
export function JobConsole({ lines, liveAfter }: { lines: LogLine[]; liveAfter: number }) {
  const box = useRef<HTMLDivElement>(null)
  const follow = useRef(true)
  useLayoutEffect(() => {
    const b = box.current
    if (b && follow.current) b.scrollTop = b.scrollHeight
  }, [lines.length])
  return (
    <div
      ref={box}
      role="log"
      aria-label="Job output"
      // biome-ignore lint/a11y/noNoninteractiveTabindex: a scrolling log takes focus, so the keyboard can scroll it
      tabIndex={0}
      className="max-h-[60dvh] min-h-40 overflow-auto overscroll-contain rounded-lg border bg-code px-4 py-3 font-mono text-[12.5px] leading-relaxed"
      onScroll={(e) => {
        const b = e.currentTarget
        follow.current = b.scrollHeight - b.scrollTop - b.clientHeight < 24
      }}
    >
      {lines.length === 0 && <p className="text-muted-foreground">No output yet.</p>}
      {lines.map((l) => {
        const text = (
          <>
            <time dateTime={l.at} className="flex-none text-muted-foreground">
              {formatTime(l.at, clock)}
            </time>
            <span className="whitespace-pre-wrap [overflow-wrap:anywhere]">{l.line}</span>
          </>
        )
        return l.id > liveAfter ? (
          <motion.div
            key={l.id}
            className="flex gap-3"
            initial={{ opacity: 0, x: -4 }}
            animate={{ opacity: 1, x: 0 }}
            transition={{ duration: 0.2 }}
          >
            {text}
          </motion.div>
        ) : (
          <div key={l.id} className="flex gap-3">
            {text}
          </div>
        )
      })}
    </div>
  )
}
