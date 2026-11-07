import type { Citation } from './code'

/** diagram: on the overview, a Mermaid flowchart of the imports between directories, drawn from the code's import graph. */
export type WikiPage = { slug: string; title: string; content: string; citations: Citation[]; diagram?: string }
export type Wiki = { snapshotId: number; commitSha: string; indexedAt: string; status: string; pages: WikiPage[] }
