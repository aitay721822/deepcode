import { ArrowLeftIcon } from 'lucide-react'
import { useCallback, useEffect, useRef } from 'react'
import { AskBox } from '../../components/chat/AskBox'
import { SearchSteps } from '../../components/chat/SearchSteps'
import { RepoNotice } from '../../components/repo/RepoNotice'
import { SkeletonText } from '../../components/ui/feedback/skeleton'
import { Link, navigate } from '../../lib/router'
import type { Citation } from '../../types/repo/code'
import { Frame } from '../Frame'
import { useRepo } from '../useRepo'
import { ChatTurn } from './ChatTurn'
import { CitedFile } from './CitedFile'
import { useChat } from './useChat'

export function SearchPage({ id, conversationId }: { id: number; conversationId?: string }) {
  const { data: repo, error, status, reload } = useRepo(id)
  const { conv, turns, selected, loadError, busy, loading, ask, select } = useChat(id, conversationId)
  const started = useRef(false)
  const aside = useRef<HTMLElement>(null)

  // On one column the cited file sits below every turn: bring it into view. Stable, or every memoised turn would draw again.
  const cite = useCallback((c: Citation) => {
    select(c, true)
    if (matchMedia('(min-width: 900px)').matches) return
    const still = matchMedia('(prefers-reduced-motion: reduce)').matches
    requestAnimationFrame(() => aside.current?.scrollIntoView({ behavior: still ? 'auto' : 'smooth', block: 'start' }))
  }, [select])

  // A question handed over from the repository page.
  useEffect(() => {
    if (!repo || conversationId || started.current) return
    const question = (history.state as { question?: string } | null)?.question
    if (!question) return navigate(`/repos/${repo.id}`, { replace: true })
    history.replaceState(null, '') // asked now: a reload must not ask it again
    started.current = true
    ask(repo, question)
  }, [repo])

  const last = turns.at(-1)
  if (!repo) {
    return (
      <Frame>
        <main>
          <RepoNotice error={error} status={status} onRetry={reload} />
        </main>
      </Frame>
    )
  }

  // The first question names the tab, which would otherwise read the same as the repository's own.
  const title = turns[0] ? `${turns[0].question} · ${repo.owner}/${repo.name}` : `${repo.owner}/${repo.name}`
  const side = last?.status === 'searching' ? <SearchSteps sources={last.sources} /> : selected && conv ? <CitedFile repo={repo} conv={conv} citation={selected} /> : null
  return (
    <Frame title={title}>
      <main tabIndex={-1} className="grid gap-4 px-4 pt-7 pb-44 outline-none min-[900px]:grid-cols-[minmax(0,0.95fr)_minmax(0,1.05fr)] min-[900px]:px-6 print:block">
        <section className="min-w-0">
          <Link to={`/repos/${repo.id}`} className="inline-flex items-center gap-1.5 font-mono text-[12.5px] [overflow-wrap:anywhere] text-muted-foreground hover:text-foreground">
            <ArrowLeftIcon className="size-3.5 shrink-0" /> {repo.owner}/{repo.name}
          </Link>
          {loadError && <p className="mt-4 text-center text-sm text-destructive" role="alert">{loadError}</p>}
          {loading && <SkeletonText className="mt-6" />}
          {turns.map((t, i) => (
            <ChatTurn key={i} turn={t} first={i === 0} repo={repo} sha={conv?.commitSha ?? ''} onCite={cite} />
          ))}
        </section>
        {/* Beside the answers the pane fills the screen under the header, but keeps 16rem: the calc alone leaves a
            short screen (a phone held sideways) a sliver. */}
        {side && (
          <aside ref={aside} className="h-[70dvh] min-w-0 print:hidden scroll-mt-20 min-[900px]:sticky min-[900px]:top-[calc(4rem+28px)] min-[900px]:h-[max(16rem,calc(100dvh-4rem-180px))]">{side}</aside>
        )}
      </main>
      {repo.permissions.includes('repo.chat') && <AskBox placeholder="Ask a follow-up question" busy={busy} disabled={loading} onAsk={(q) => ask(repo, q)} />}
    </Frame>
  )
}
