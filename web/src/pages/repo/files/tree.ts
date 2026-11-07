import type { FileEntry } from '../../../types/repo/code'

export type Dir = { dirs: Map<string, Dir>; files: string[] }

/** Nests the flat file list into folders. */
export function buildTree(files: FileEntry[]) {
  const root: Dir = { dirs: new Map(), files: [] }
  for (const f of files) {
    const parts = f.path.split('/')
    const name = parts.pop() ?? f.path // split always gives at least one part
    let dir = root
    for (const p of parts) {
      let sub = dir.dirs.get(p)
      if (!sub) {
        sub = { dirs: new Map(), files: [] }
        dir.dirs.set(p, sub)
      }
      dir = sub
    }
    dir.files.push(name)
  }
  return root
}

/** The folders that hold path, outermost first: "a/b/c.go" → a, a/b. */
export const ancestors = (path: string | null) =>
  path
    ? path
        .split('/')
        .slice(0, -1)
        .map((_, i, a) => a.slice(0, i + 1).join('/'))
    : []
