import { CheckIcon, CopyIcon, XIcon } from 'lucide-react'
import { memo, useEffect, useState } from 'react'
import { getFile } from '../../api/repo/code'
import { providerName } from '../../components/repo/provider/providerName'
import { sourceUrl } from '../../components/repo/sourceLinks'
import { CodeView } from '../../components/source/CodeView'
import { Button } from '../../components/ui/controls/button'
import { LoadError } from '../../components/ui/feedback/load-error'
import { SkeletonText } from '../../components/ui/feedback/skeleton'
import { useApi } from '../../lib/useApi'
import type { ConversationRef } from '../../types/repo/chat'
import type { Citation } from '../../types/repo/code'
import type { Repo } from '../../types/repo/repo'

/**
 * The cited lines in their file, at the snapshot the conversation is pinned to.
 * Memoised: an answer streaming beside it does not draw the whole file again on every frame.
 */
export const CitedFile = memo(function CitedFile({
  repo,
  conv,
  citation,
}: {
  repo: Repo
  conv: ConversationRef
  citation: Citation
}) {
  const { data, error, reload } = useApi(`${repo.id}:${conv.snapshotId}:${citation.path}`, () =>
    getFile(repo.id, citation.path, conv.snapshotId),
  )
  const [copy, setCopy] = useState<'' | 'Copied' | 'Copy failed'>('')
  useEffect(() => {
    if (!copy) return
    const t = setTimeout(() => setCopy(''), 1500) // a transient tick, or cross
    return () => clearTimeout(t)
  }, [copy])
  return (
    <div className="flex h-full flex-col overflow-hidden rounded-lg border bg-code">
      <div className="flex min-h-11 items-center gap-2.5 border-b bg-card py-1 pr-2 pl-4 font-mono text-[12.5px]">
        <span className="max-w-[40%] truncate text-muted-foreground">
          {repo.owner}/{repo.name}
        </span>
        <span className="min-w-0 flex-1 truncate">{citation.path}</span>
        <Button
          variant="ghost"
          size="icon-sm"
          className="text-muted-foreground"
          aria-label="Copy path"
          onClick={async () => {
            try {
              await navigator.clipboard.writeText(citation.path) // no clipboard outside https or localhost, or permission denied
              setCopy('Copied')
            } catch {
              setCopy('Copy failed')
            }
          }}
        >
          {copy === 'Copied' ? (
            <CheckIcon className="size-3.5" />
          ) : copy ? (
            <XIcon className="size-3.5" />
          ) : (
            <CopyIcon className="size-3.5" />
          )}
        </Button>
        <span className="sr-only" role="status">
          {copy}
        </span>
      </div>
      {error ? (
        <LoadError className="m-0 p-4" error={error} onRetry={reload}>
          <a
            className="text-sm text-brand underline-offset-4 hover:underline"
            href={sourceUrl(repo, conv.commitSha, citation.path, citation.startLine, citation.endLine)}
            target="_blank"
            rel="noreferrer"
          >
            Open on {providerName(repo.provider)}
          </a>
        </LoadError>
      ) : data ? (
        <CodeView
          label={citation.path}
          content={data.content}
          language={data.language}
          startLine={citation.startLine}
          endLine={citation.endLine}
        />
      ) : (
        <SkeletonText className="px-4" />
      )}
    </div>
  )
})
