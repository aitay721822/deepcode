import type { Wiki } from '../../types/repo/wiki'
import { api, httpError, unreachable } from '../client'

/** The wiki to show: the newest finished one, which can trail the published snapshot while the next is written. */
export const getWiki = (repoId: string) => api<Wiki>(`/api/repos/${repoId}/wiki`)

/** The shown wiki as a zip of Markdown pages and a README index, at its commit. */
export async function exportWiki(repoId: string): Promise<Blob> {
  const res = await fetch(`/api/repos/${repoId}/wiki/export`, { signal: AbortSignal.timeout(60_000) }).catch(
    unreachable,
  )
  if (!res.ok) throw await httpError(res)
  return res.blob()
}
