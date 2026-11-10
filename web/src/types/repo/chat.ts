import type { Citation } from './code'

/** How a question is answered: fast, deep (more search rounds and the wiki first), or drawn as a code map. */
export type AskMode = 'fast' | 'deep' | 'codemap'

/** A line of code a code map points at, lettered in its trace: 1a, 1b, … */
export type CodemapLocation = {
  id: string
  title: string
  description: string
  path: string
  line: number
  code: string
}

/** One path through the code, its locations in the order it runs; guide is Markdown on how they work together. */
export type CodemapTrace = {
  id: string
  title: string
  description: string
  guide: string
  locations: CodemapLocation[]
}

/** An answer drawn as a code map, as DeepWiki's Codemap mode draws one: numbered traces of code locations. */
export type Codemap = { title: string; description: string; traces: CodemapTrace[] }

/** codemap: an answer drawn as a code map, whose content is the map as text. */
export type Message = {
  id: number
  role: 'user' | 'assistant'
  content: string
  citations: Citation[]
  codemap?: Codemap
}
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

/** One call of the model's while it gathers an answer's code: a search, a file read, or the code that uses a name. */
export type SearchStep =
  | { round: number; tool: 'search_code'; args: { query: string; path_prefix: string } }
  | { round: number; tool: 'read_file'; args: { path: string; start_line: number; end_line: number } }
  | { round: number; tool: 'users'; args: { name: string } }

/**
 * One server-sent event of a streamed answer: conversation, search (each call of the model's while it gathers the
 * code), evidence (what the answer is written from), delta…, done (or error at any point). A code map sends drawing…
 * (how much of it has come) in place of delta….
 */
export type ChatEvent =
  | { event: 'conversation'; data: ConversationRef }
  | { event: 'search'; data: SearchStep }
  | { event: 'evidence'; data: { sources: Source[]; rounds: number } }
  | { event: 'delta'; data: string }
  | { event: 'drawing'; data: { chars: number } }
  | { event: 'done'; data: Message }
  | { event: 'error'; data: { message: string } }
