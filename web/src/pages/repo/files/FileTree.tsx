import { ChevronRightIcon, FileIcon, FolderIcon } from 'lucide-react'
import { type ReactNode, useEffect, useMemo, useState } from 'react'
import { cn } from '../../../lib/style/cn'
import type { FileEntry } from '../../../types/repo/code'
import { ancestors, buildTree, type Dir } from './tree'

const row =
  'flex w-full items-center gap-1.5 rounded-sm px-1.5 py-[3px] text-left font-mono text-[12.5px] whitespace-nowrap text-muted-foreground hover:bg-accent/50 hover:text-foreground'

export function FileTree({
  files,
  active,
  onOpen,
}: {
  files: FileEntry[]
  active: string | null
  onOpen: (path: string) => void
}) {
  const root = useMemo(() => buildTree(files), [files])
  const [open, setOpen] = useState(() => new Set(ancestors(active)))
  useEffect(() => setOpen((o) => new Set([...o, ...ancestors(active)])), [active])
  const toggle = (p: string) =>
    setOpen((o) => {
      const n = new Set(o)
      if (!n.delete(p)) n.add(p)
      return n
    })
  const render = (dir: Dir, prefix: string, depth: number): ReactNode[] => [
    ...[...dir.dirs.keys()].sort().map((d) => {
      const path = prefix + d
      const sub = dir.dirs.get(d)
      return (
        <li key={path}>
          <button
            type="button"
            className={row}
            style={{ paddingLeft: 6 + depth * 14 }}
            aria-expanded={open.has(path)}
            onClick={() => toggle(path)}
          >
            <ChevronRightIcon className={cn('size-3 shrink-0 transition-transform', open.has(path) && 'rotate-90')} />
            <FolderIcon className="size-3.5 shrink-0" />
            {d}
          </button>
          {sub && open.has(path) && <ul>{render(sub, `${path}/`, depth + 1)}</ul>}
        </li>
      )
    }),
    ...[...dir.files].sort().map(
      (
        f, // copy: the tree is memoized
      ) => (
        <li key={prefix + f}>
          <button
            type="button"
            className={cn(
              row,
              active === prefix + f &&
                'bg-accent font-medium text-foreground hover:bg-accent forced-colors:border [&>svg]:text-brand',
            )}
            style={{ paddingLeft: 24 + depth * 14 }}
            aria-current={active === prefix + f ? 'page' : undefined}
            onClick={() => onOpen(prefix + f)}
          >
            <FileIcon className="size-3.5 shrink-0" />
            {f}
          </button>
        </li>
      ),
    ),
  ]
  return <ul>{render(root, '', 0)}</ul>
}
