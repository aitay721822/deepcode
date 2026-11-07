import { motion } from 'motion/react'
import { memo, useState } from 'react'
import { linkMarkers } from '../../components/source/citations'
import { Markdown } from '../../components/source/Markdown'
import { Card } from '../../components/ui/display/card'
import { SkeletonText } from '../../components/ui/feedback/skeleton'
import { Status } from '../../components/ui/feedback/status'
import { rise } from '../../lib/style/motion'
import type { Citation } from '../../types/repo/code'
import type { Repo } from '../../types/repo/repo'
import type { Turn } from './useChat'

const question = 'mb-4 font-serif leading-[1.15] font-normal [overflow-wrap:anywhere]'

// What a screen reader hears while a question asked here is answered: the answer itself streams in
// too fast to be read out as it comes. Errors and interruptions speak for themselves below.
const progress: Partial<Record<Turn['status'], string>> = {
  searching: 'Searching the code…',
  answering: 'Writing the answer…',
  done: 'Answer ready.',
}

/**
 * One question and its answer, linked as it streams in (sha: the conversation's commit).
 * Memoised: while the last answer streams, the turns above it are not parsed again on every frame.
 */
export const ChatTurn = memo(function ChatTurn({
  turn,
  first,
  repo,
  sha,
  onCite,
}: {
  turn: Turn
  first: boolean
  repo: Repo
  sha: string
  onCite: (c: Citation) => void
}) {
  const [asked] = useState(turn.status === 'searching') // asked on this page, not reopened with the conversation
  // A finished answer as the server linked it and checked its citations. One still streaming is linked here meanwhile, its
  // markers shown as citations until the server's list replaces them; one that never finished keeps its links, as plain links.
  const shown =
    turn.status === 'done' ? { text: turn.answer, citations: turn.citations } : linkMarkers(turn.answer, repo, sha)
  const cited = turn.status === 'error' || turn.status === 'interrupted' ? [] : shown.citations
  return (
    <motion.article initial={rise.initial} animate={rise.animate}>
      {first ? (
        <h1 className={`mt-5 text-[clamp(1.75rem,3vw,2.3rem)] tracking-[-0.02em] ${question}`}>{turn.question}</h1>
      ) : (
        <h2 className={`mt-11 text-[25px] tracking-[-0.015em] ${question}`}>{turn.question}</h2>
      )}
      <Card className="gap-0 px-5 py-4">
        {asked && <Status className="sr-only select-none">{progress[turn.status]}</Status>}
        {turn.answer ? (
          <Markdown text={shown.text} citations={cited} repo={repo} onCite={onCite} top={first ? 'h2' : 'h3'} />
        ) : turn.status === 'searching' || turn.status === 'answering' ? (
          <SkeletonText />
        ) : null}
        {turn.status === 'error' && (
          <p className="text-sm text-destructive" role={asked ? 'alert' : undefined}>
            {turn.error}
          </p>
        )}
        {turn.status === 'interrupted' && (
          <Status className="text-[13px] text-muted-foreground">Answer interrupted.</Status>
        )}
      </Card>
    </motion.article>
  )
})
