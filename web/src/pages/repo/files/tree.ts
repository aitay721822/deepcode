import type { FileEntry } from '../../../types/repo/code'

export type Dir = { dirs: Map<string, Dir>; files: string[] }

/** Nests the flat file list into folders. */
export function buildTree(files: FileEntry[]) {
  const root: Dir = { dirs: new Map(), files: [] }
  for (const f of files) {
    const parts = f.path.split('/')
    let dir = root
    for (const p of parts.slice(0, -1)) {
      if (!dir.dirs.has(p)) dir.dirs.set(p, { dirs: new Map(), files: [] })
      dir = dir.dirs.get(p)!
    }
    dir.files.push(parts.at(-1)!)
  }
  return root
}

/** The folders that hold path, outermost first: "a/b/c.go" → a, a/b. */
export const ancestors = (path: string | null) => (path ? path.split('/').slice(0, -1).map((_, i, a) => a.slice(0, i + 1).join('/')) : [])
