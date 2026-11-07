import type { Repo } from '../../../types/repo/repo'
import { ProviderIcon } from './ProviderIcon'
import { providerName } from './providerName'

/** The git server a repository comes from: its provider's mark and the server's name. */
export function ProviderBadge({ repo }: { repo: Pick<Repo, 'provider' | 'serverName'> }) {
  return (
    <span
      className="inline-flex min-w-0 items-center gap-1.5 font-mono text-[11px] text-muted-foreground"
      title={`${providerName(repo.provider)}: ${repo.serverName}`}
    >
      <ProviderIcon provider={repo.provider} className="size-3 flex-none" />
      <span className="truncate">{repo.serverName}</span>
    </span>
  )
}
