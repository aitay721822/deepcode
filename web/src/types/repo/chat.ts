import type { Citation } from './code'

export type Message = { id: number; role: 'user' | 'assistant'; content: string; citations: Citation[] }
export type Conversation = {
  id: number
  repoId: string
  snapshotId: number
  commitSha: string
  title: string
  messages: Message[]
}
export type Source = { path: string; symbol: string; startLine: number; endLine: number }

/** The conversation an answer belongs to, and the snapshot it is pinned to. */
export type ConversationRef = Pick<Conversation, 'id' | 'snapshotId' | 'commitSha'>

/**
 * One server-sent event of a streamed answer: conversation, search (each further round of a deep answer), evidence,
 * delta…, done (or error at any point).
 */
export type ChatEvent =
  | { event: 'conversation'; data: ConversationRef }
  | { event: 'search'; data: { round: number; terms: string } }
  | { event: 'evidence'; data: { sources: Source[]; rounds: number } }
  | { event: 'delta'; data: string }
  | { event: 'done'; data: Message }
  | { event: 'error'; data: { message: string } }
