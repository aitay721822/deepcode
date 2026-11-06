import type { Citation } from './code'

export type Message = { id: number; role: 'user' | 'assistant'; content: string; citations: Citation[] }
export type Conversation = { id: number; repoId: number; snapshotId: number; commitSha: string; title: string; messages: Message[] }
export type Source = { path: string; symbol: string; startLine: number; endLine: number }

/** The conversation an answer belongs to, and the snapshot it is pinned to. */
export type ConversationRef = Pick<Conversation, 'id' | 'snapshotId' | 'commitSha'>

/** One server-sent event of a streamed answer: conversation, evidence, delta…, done (or error at any point). */
export type ChatEvent =
  | { event: 'conversation'; data: ConversationRef }
  | { event: 'evidence'; data: { sources: Source[] } }
  | { event: 'delta'; data: string }
  | { event: 'done'; data: Message }
  | { event: 'error'; data: { message: string } }
