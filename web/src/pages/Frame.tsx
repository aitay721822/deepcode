import { type ReactNode, useEffect } from 'react'
import type { Repo } from '../types/repo/repo'
import { SiteHeader } from './SiteHeader'

/** The page shell: the site header (with the repository, when there is one) above the page; title (else the repository) names the tab. */
export function Frame({ repo, title, children }: { repo?: Repo; title?: string; children: ReactNode }) {
  const name = title ?? (repo && `${repo.owner}/${repo.name}`)
  useEffect(() => {
    document.title = name ? `${name} · deepcode` : 'deepcode'
  }, [name])
  return (
    <div className="mx-auto min-h-dvh max-w-[1540px] border-x">
      <SiteHeader repo={repo} />
      {children}
    </div>
  )
}
