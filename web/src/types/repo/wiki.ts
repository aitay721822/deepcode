import type { Citation } from './code'

export type WikiPage = { slug: string; title: string; content: string; citations: Citation[] }
export type Wiki = { snapshotId: number; commitSha: string; indexedAt: string; status: string; pages: WikiPage[] }
