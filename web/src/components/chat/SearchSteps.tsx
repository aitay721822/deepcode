import { motion } from 'motion/react'
import type { Source, Step } from '../../types/repo/chat'
import { Card } from '../ui/display/card'
import { Progress } from '../ui/feedback/progress'

const step =
  "flex items-center gap-2.5 py-1 text-muted-foreground before:text-brand before:content-['›'] after:min-w-6 after:flex-1 after:border-b after:border-dashed after:content-['']"

/**
 * What a step did, in a line: what a call of the model's searched for, which lines it read, whose users it found; or
 * how a request was made smaller to fit the model's context.
 */
function describe(s: Step): string {
  if ('masked' in s) return `已遮蔽 ${s.masked} 段較早的搜尋結果`
  if ('summarized' in s) return '較早的對話已整理成摘要'
  switch (s.tool) {
    case 'search_code':
      return `搜尋：${s.args.query}${s.args.path_prefix ? `（於 ${s.args.path_prefix}）` : ''}`
    case 'read_file': {
      const { path, start_line: from, end_line: to } = s.args
      return `讀取 ${path}${from && to ? `（第 ${from}-${to} 行）` : from ? `（自第 ${from} 行）` : ''}`
    }
    case 'users':
      return `找出使用 ${s.args.name} 的程式碼`
  }
}

/** The retrieval trail shown while an answer is being prepared: what the model searched for and read, then the sources. */
export function SearchSteps({ sources, steps }: { sources: Source[]; steps: Step[] }) {
  return (
    <Card asChild className="gap-4 px-5 py-[18px]">
      <section aria-live="polite">
        <p className="font-serif text-[20px] leading-tight italic">正在搜尋程式碼</p>
        <Progress tone="warning" aria-label="正在搜尋程式碼" />
        <ul className="font-mono text-[12.5px]">
          {steps.map((s, i) => (
            <motion.li
              // biome-ignore lint/suspicious/noArrayIndexKey: steps are only appended, and two may be alike
              key={`s${i}`}
              className={step}
              initial={{ opacity: 0, x: -6 }}
              animate={{ opacity: 1, x: 0, transition: { duration: 0.2 } }}
            >
              <span className="[overflow-wrap:anywhere]">{describe(s)}</span>
            </motion.li>
          ))}
          {sources.map((s, i) => (
            <motion.li
              // biome-ignore lint/suspicious/noArrayIndexKey: set once per question, and two sources may share a path and first line
              key={i}
              className={step}
              initial={{ opacity: 0, x: -6 }}
              animate={{ opacity: 1, x: 0, transition: { delay: Math.min(i, 8) * 0.04, duration: 0.2 } }}
            >
              <span className="[overflow-wrap:anywhere]">
                已讀 {s.path.split('/').pop()}（第 {s.startLine}-{s.endLine} 行）
              </span>
            </motion.li>
          ))}
        </ul>
      </section>
    </Card>
  )
}
