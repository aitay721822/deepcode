import { encodePath } from '../../lib/url'
import type { FileContent, FileEntry, SymbolHit } from '../../types/repo/code'
import { api } from '../client'

export const listFiles = (repoId: number, snapshotId: number) => api<FileEntry[]>(`/api/repos/${repoId}/files?snapshot=${snapshotId}`)

/** A file as of the given snapshot (none: the published one). A path with . or .. segments, which the URL would resolve away, is not found. */
export const getFile = (repoId: number, path: string, snapshot?: string | number | null) =>
  path.split('/').some((s) => s === '.' || s === '..')
    ? Promise.reject(Object.assign(new Error('file not found in this snapshot'), { status: 404 }))
    : api<FileContent>(`/api/repos/${repoId}/files/${encodePath(path)}${snapshot ? `?snapshot=${encodeURIComponent(snapshot)}` : ''}`)

export const searchSymbols = (repoId: number, snapshotId: number, query: string) =>
  api<SymbolHit[]>(`/api/repos/${repoId}/symbols?snapshot=${snapshotId}&q=${encodeURIComponent(query)}`)
