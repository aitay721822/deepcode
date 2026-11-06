import { useEffect, useState } from 'react'
import { listFiles, searchSymbols } from '../../../api/repo/code'
import { SkeletonText } from '../../../components/ui/feedback/skeleton'
import { Input } from '../../../components/ui/form/input'
import { useApi } from '../../../lib/useApi'
import type { Repo } from '../../../types/repo/repo'
import { FileTree } from './FileTree'

/** The published snapshot's file tree, or its symbols matching the filter. */
export function FilesPanel({ repo, active, onOpen }: { repo: Repo; active: string | null; onOpen: (path: string, start?: number, end?: number) => void }) {
  const snapshot = repo.snapshot!.id
  const { data: files, error } = useApi(`${repo.id}:${snapshot}`, () => listFiles(repo.id, snapshot))
  const [query, setQuery] = useState('')
  const [term, setTerm] = useState('')
  useEffect(() => {
    const t = setTimeout(() => setTerm(query.trim()), 200) // debounce typing
    return () => clearTimeout(t)
  }, [query])
  const { data: symbols, error: symbolError } = useApi(term ? `${repo.id}:${snapshot}:${term}` : null, () => searchSymbols(repo.id, snapshot, term))
  return (
    <div>
      <Input className="mb-3 h-8 font-mono md:text-[12.5px]" value={query} placeholder="Search symbols" aria-label="Search symbols" onChange={(e) => setQuery(e.target.value)} />
      {term && symbols?.length ? (
        <ul>
          {symbols.map((s) => (
            <li key={`${s.path}:${s.startLine}:${s.qualifiedName}`}>
              <button className="grid w-full grid-cols-[auto_minmax(0,1fr)] gap-x-2 gap-y-0.5 rounded-sm p-1.5 text-left hover:bg-accent"
                onClick={() => onOpen(s.path, s.startLine, s.endLine)} title={s.signature}>
                <span className="row-span-2 self-start rounded-sm border border-brand/40 px-1.5 font-mono text-[10.5px] leading-normal text-brand">{s.kind}</span>
                <span className="truncate font-mono text-[12.5px]">{s.qualifiedName}</span>
                <span className="truncate font-mono text-[11px] text-muted-foreground">{s.path}:{s.startLine}</span>
              </button>
            </li>
          ))}
        </ul>
      ) : term ? (
        symbols ? <p className="text-[13px] text-muted-foreground">No symbols match.</p>
          : symbolError ? <p className="text-sm text-destructive" role="alert">{symbolError}</p>
          : <SkeletonText />
      ) : files?.length ? (
        <FileTree files={files} active={active} onOpen={onOpen} />
      ) : files ? (
        <p className="text-[13px] text-muted-foreground">No files were indexed.</p>
      ) : error ? (
        <p className="text-sm text-destructive" role="alert">{error}</p>
      ) : (
        <SkeletonText />
      )}
    </div>
  )
}
