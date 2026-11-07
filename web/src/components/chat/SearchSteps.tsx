import { motion } from 'motion/react'
import type { Source } from '../../types/repo/chat'
import { Card } from '../ui/display/card'
import { Progress } from '../ui/feedback/progress'

const step = "flex items-center gap-2.5 py-1 text-muted-foreground before:text-brand before:content-['›'] after:min-w-6 after:flex-1 after:border-b after:border-dashed after:content-['']"

/** The retrieval trail shown while an answer is being prepared: a deep answer's further searches, then what was read. */
export function SearchSteps({ sources, rounds }: { sources: Source[]; rounds: string[] }) {
  return (
    <Card asChild className="gap-4 px-5 py-[18px]">
      <section aria-live="polite">
        <p className="font-serif text-[20px] leading-tight italic">Searching the codebase</p>
        <Progress tone="warning" aria-label="Searching the codebase" />
        <ul className="font-mono text-[12.5px]">
          {rounds.map((terms, i) => (
            <motion.li key={`r${i}`} className={step} initial={{ opacity: 0, x: -6 }} animate={{ opacity: 1, x: 0, transition: { duration: 0.2 } }}>
              <span className="[overflow-wrap:anywhere]">Searching again for {terms}</span>
            </motion.li>
          ))}
          {sources.map((s, i) => (
            <motion.li key={i} // set once per question, and two sources may share a path and first line
              className={step}
              initial={{ opacity: 0, x: -6 }} animate={{ opacity: 1, x: 0, transition: { delay: Math.min(i, 8) * 0.04, duration: 0.2 } }}>
              <span className="[overflow-wrap:anywhere]">Read {s.path.split('/').pop()} (lines {s.startLine}-{s.endLine})</span>
            </motion.li>
          ))}
        </ul>
      </section>
    </Card>
  )
}
