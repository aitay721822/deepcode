import type { Citation } from './code'

/**
 * files: the files its plan gives it, those the model chose first; none for a page planned without.
 * diagram: on the overview, a Mermaid flowchart of the imports between directories, drawn from the code's import graph.
 * parent: the slug of the section page this one is under; none at the top.
 */
export type WikiPage = {
  slug: string
  title: string
  content: string
  citations: Citation[]
  files?: string[]
  diagram?: string
  parent?: string
}
export type Wiki = { snapshotId: number; commitSha: string; indexedAt: string; status: string; pages: WikiPage[] }
