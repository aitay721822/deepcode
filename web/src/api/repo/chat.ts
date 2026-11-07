import type { ChatEvent, Conversation } from '../../types/repo/chat'
import { api, httpError, unreachable } from '../client'

export const getConversation = (id: string) => api<Conversation>(`/api/conversations/${id}`)

/**
 * POSTs a question (conversationId 0 starts one; deep searches more rounds and reads the wiki first) and reports each
 * server-sent event (EventSource cannot POST).
 */
export async function streamChat(
  body: { repoId: number; conversationId: number; question: string; deep: boolean },
  on: (e: ChatEvent) => void,
  signal: AbortSignal,
) {
  const res = await fetch('/api/chat/stream', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  }).catch(unreachable)
  if (!res.ok || !res.body) throw await httpError(res)
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader()
  let buf = ''
  for (;;) {
    const { value, done } = await reader.read().catch(unreachable)
    if (done) return
    buf += value
    let i: number
    while ((i = buf.indexOf('\n\n')) >= 0) {
      const block = buf.slice(0, i)
      buf = buf.slice(i + 2)
      let event = 'message'
      let data = ''
      for (const line of block.split('\n')) {
        if (line.startsWith('event: ')) event = line.slice(7)
        else if (line.startsWith('data: ')) data += line.slice(6)
      }
      if (data) on({ event, data: JSON.parse(data) } as ChatEvent)
    }
  }
}
