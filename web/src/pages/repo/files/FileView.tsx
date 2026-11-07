import { ExternalLinkIcon } from 'lucide-react'
import { getFile } from '../../../api/repo/code'
import { providerName } from '../../../components/repo/provider/providerName'
import { sourceUrl } from '../../../components/repo/sourceLinks'
import { CodeView } from '../../../components/source/CodeView'
import { LoadError } from '../../../components/ui/feedback/load-error'
import { SkeletonText } from '../../../components/ui/feedback/skeleton'
import { useApi } from '../../../lib/useApi'
import type { Repo } from '../../../types/repo/repo'

// The code fills the screen below the header and the file bar, but keeps at least 16rem: a phone held
// sideways (375px) has 311px under the header, and the code would get a sliver of it. Paper takes it all.
const codeHeight = 'max-h-[max(16rem,calc(100dvh-4rem-270px))] print:max-h-none'

/** A file at a snapshot; sha is the commit the caller knows it at, for the provider link when the file cannot load. */
export function FileView({
  repo,
  path,
  lines,
  snapshot,
  sha,
}: {
  repo: Repo
  path: string
  lines?: number[]
  snapshot: string | null
  sha?: string
}) {
  const { data, error, reload } = useApi(`${repo.id}:${snapshot || repo.snapshot?.id}:${path}`, () =>
    getFile(repo.id, path, snapshot),
  )
  const commit = data?.commitSha ?? sha
  const cut = path.lastIndexOf('/') + 1 // on a narrow screen the folders give way, the file name stays
  const link = commit && (
    <a
      className="inline-flex items-center gap-1 text-[12.5px] whitespace-nowrap text-brand underline-offset-4 hover:underline"
      href={sourceUrl(repo, commit, path, lines?.[0], lines?.[1])}
      target="_blank"
      rel="noreferrer"
    >
      Open on {providerName(repo.provider)} <ExternalLinkIcon className="size-[13px]" />
    </a>
  )
  return (
    <section className="overflow-hidden rounded-lg border bg-code print:overflow-visible">
      <div className="flex min-h-11 items-center gap-2.5 border-b bg-card py-1 pr-3 pl-4">
        <h1 className="flex min-w-0 flex-1 font-mono text-[12.5px]" title={path}>
          <span className="min-w-0 truncate">{path.slice(0, cut)}</span>
          <span className="max-w-full shrink-0 truncate">{path.slice(cut)}</span>
        </h1>
        {link}
      </div>
      {error ? (
        <LoadError className="m-0 p-4" error={error} onRetry={reload} />
      ) : data ? (
        <CodeView
          label={path}
          className={codeHeight}
          content={data.content}
          language={data.language}
          startLine={lines?.[0]}
          endLine={lines?.[1]}
        />
      ) : (
        <SkeletonText className="px-4" />
      )}
    </section>
  )
}
