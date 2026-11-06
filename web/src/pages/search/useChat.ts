import { useCallback, useEffect, useRef, useState } from 'react'
import { getConversation, streamChat } from '../../api/repo/chat'
import { navigate } from '../../lib/router'
import type { ConversationRef, Message, Source } from '../../types/repo/chat'
import type { Citation } from '../../types/repo/code'
import type { Repo } from '../../types/repo/repo'

export type Turn = {
  question: string
  answer: string
  citations: Citation[]
  sources: Source[]
  status: 'searching' | 'answering' | 'done' | 'error' | 'interrupted' // interrupted: the stream closed before done or error
  error?: string
}

function toTurns(messages: Message[]): Turn[] {
  const turns: Turn[] = []
  for (const m of messages) {
    if (m.role === 'user') {
      turns.push({ question: m.content, answer: '', citations: [], sources: [], status: 'error', error: 'This question has no stored answer.' })
    } else if (turns.length) {
      Object.assign(turns[turns.length - 1], { answer: m.content, citations: m.citations, status: 'done', error: undefined })
    }
  }
  return turns
}

/** A conversation about repository repoId: opens a stored one, streams new answers and picks the citation to show. */
export function useChat(repoId: number, conversationId?: string) {
  const [conv, setConv] = useState<ConversationRef | null>(null)
  const [turns, setTurns] = useState<Turn[]>([])
  const [selected, setSelected] = useState<Citation | null>(null)
  const [loadError, setLoadError] = useState('')
  const pinned = useRef(false) // the reader picked a citation: stop auto-selecting
  const abort = useRef<AbortController | null>(null)
  useEffect(() => () => abort.current?.abort(), [])

  // Open a stored conversation (but not the one this page just created).
  useEffect(() => {
    if (!conversationId || String(conv?.id) === conversationId) return
    abort.current?.abort() // another conversation: stop the answer streaming into this one
    let live = true // back/forward to another conversation: drop this one's late answer
    setConv(null) // until it loads, a follow-up must not join the one on screen
    setTurns([]) // nor may its turns stand under this one's address
    setLoadError('')
    getConversation(conversationId).then((c) => {
      if (!live) return
      if (c.repoId !== repoId) return setLoadError('conversation not found') // another repository's: as the server says of one the reader cannot open
      setConv({ id: c.id, snapshotId: c.snapshotId, commitSha: c.commitSha })
      setTurns(toTurns(c.messages))
      setSelected(c.messages.find((m) => m.citations?.length)?.citations[0] ?? null)
    }, (e: Error) => live && setLoadError(e.message))
    return () => {
      live = false
    }
  }, [conversationId])

  const update = (fn: (t: Turn) => Turn) => setTurns((ts) => ts.map((t, i) => (i === ts.length - 1 ? fn(t) : t)))
  // Stable: the page hands it to the turns, which are memoised.
  const select = useCallback((c: Citation | undefined, byReader = false) => {
    if (!c || (pinned.current && !byReader)) return
    pinned.current ||= byReader
    setSelected(c)
  }, [])

  async function ask(repo: Repo, question: string) {
    pinned.current = false
    setLoadError('') // a new question: the stored conversation's load error no longer applies
    setTurns((ts) => [...ts, { question, answer: '', citations: [], sources: [], status: 'searching' }])
    const ctl = new AbortController()
    abort.current = ctl
    // The answer re-renders as markdown, so streamed text lands at most once per frame, not per token.
    let text = ''
    let frame = 0
    const flush = () => {
      cancelAnimationFrame(frame)
      frame = 0
      const more = text
      text = ''
      if (more) update((t) => ({ ...t, status: 'answering', answer: t.answer + more }))
    }
    try {
      await streamChat({ repoId: repo.id, conversationId: conv?.id ?? 0, question }, ({ event, data }) => {
        if (event === 'conversation') {
          setConv(data)
          if (String(data.id) !== conversationId) navigate(`/repos/${repo.id}/search/${data.id}`, { replace: true })
        } else if (event === 'evidence') {
          update((t) => ({ ...t, sources: data.sources }))
          select(data.sources[0])
        } else if (event === 'delta') {
          text += data
          frame ||= requestAnimationFrame(flush)
        } else if (event === 'done') {
          flush()
          update((t) => ({ ...t, status: 'done', answer: data.content, citations: data.citations }))
          select(data.citations[0])
        } else if (event === 'error') {
          flush()
          update((t) => ({ ...t, status: 'error', error: data.message }))
        }
      }, ctl.signal)
    } catch (e) {
      if (ctl.signal.aborted) return cancelAnimationFrame(frame) // left the page or this conversation
      flush()
      update((t) => ({ ...t, status: 'error', error: (e as Error).message }))
    }
    // Closed early (session ended, access lost, server gone): keep what arrived.
    flush()
    update((t) => (t.status === 'searching' || t.status === 'answering' ? { ...t, status: 'interrupted' } : t))
  }

  const last = turns.at(-1)
  const busy = last?.status === 'searching' || last?.status === 'answering'
  // Wait for a stored conversation to load before a follow-up joins it; otherwise
  // (a new page, a bad link, or once a question was asked here) asking starts afresh.
  const loading = !!conversationId && !conv && !loadError && !turns.length
  return { conv, turns, selected, loadError, busy, loading, ask, select }
}
