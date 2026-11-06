import { decode, encodePath } from '../../lib/url'
import type { Citation } from '../../types/repo/code'
import type { Repo } from '../../types/repo/repo'

export type CiteTarget = Citation & { sha: string }

const root = (repo: Pick<Repo, 'url'>) => repo.url.replace(/\/+$/, '')

/**
 * The provider's page for path at sha, with the lines highlighted; the same links the server writes for citations:
 * github `{url}/blob/{sha}/{path}#L1-L5`, gitlab `{url}/-/blob/{sha}/{path}#L1-5`.
 */
export function sourceUrl(repo: Pick<Repo, 'url' | 'provider'>, sha: string, path: string, start?: number, end?: number) {
  const gitlab = repo.provider === 'gitlab'
  const lines = start ? `#L${start}-${gitlab ? '' : 'L'}${end ?? start}` : ''
  return `${root(repo)}${gitlab ? '/-/blob/' : '/blob/'}${sha}/${encodePath(path)}${lines}`
}

const tail = /^([0-9a-f]{7,64})\/(.+?)#L(\d+)(?:-L?(\d+))?$/i

/** Recognises a citation link into repo, in either provider's form (host compared case-insensitively); null for any other link. */
export function parseCitation(href: string | undefined, repo: Pick<Repo, 'url'>): CiteTarget | null {
  if (!href) return null
  const prefix = [`${root(repo)}/blob/`, `${root(repo)}/-/blob/`].find((p) => href.slice(0, p.length).toLowerCase() === p.toLowerCase())
  const m = prefix ? tail.exec(href.slice(prefix.length)) : null
  if (!m) return null
  return {
    sha: m[1],
    path: m[2].split('/').map(decode).join('/'),
    startLine: +m[3],
    endLine: m[4] ? +m[4] : +m[3],
  }
}
