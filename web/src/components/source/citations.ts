import type { Citation } from '../../types/repo/code'
import type { Repo } from '../../types/repo/repo'
import { sourceUrl } from '../repo/sourceLinks'

// The same grammar as the server's parser (internal/knowledge/evidence/citations.go).
const groupRe = /\[([^[\]\n]+)\](?!\()/g
const refRe = /^`?([^\s`]+?):L?(\d+)(?:-L?(\d+))?`?$/
const rangeRe = /^L?\d+(?:-L?\d+)?$/
const fenceRe = /(```[\s\S]*?(?:```|$))/

/** Links [path:10-20] groups while an answer streams, the way the server will once it has checked them; with the citations linked. */
export function linkMarkers(text: string, repo: Pick<Repo, 'url' | 'provider'>, sha: string): { text: string; citations: Citation[] } {
  const citations: Citation[] = []
  if (!sha) return { text, citations }
  const link = (c: Citation) => {
    citations.push(c)
    return `[${c.path}:${c.startLine}-${c.endLine}](${sourceUrl(repo, sha, c.path, c.startLine, c.endLine)})`
  }
  const linked = text
    .split(fenceRe) // odd parts are code blocks, where links do not render
    .map((part, i) => (i % 2 ? part : part.replace(groupRe, (m, group: string) => parseRefs(group)?.map(link).join(' ') ?? m)))
    .join('')
  return { text: linked, citations }
}

/** Reads [a.go:1-5; b.go:7, 9-10]; null unless every part is a file reference. */
function parseRefs(group: string): Citation[] | null {
  const refs: Citation[] = []
  for (const raw of group.split(/[,;]/).filter(Boolean)) {
    let part = raw.replaceAll('\\_', '_').trim()
    const prev = refs.at(-1)
    if (prev && rangeRe.test(part)) part = `${prev.path}:${part}`
    const m = part.match(refRe)
    const path = m?.[1].replace(/^\.\//, '').replace(/^\//, '')
    if (!m || !path || !/[./]/.test(path)) return null
    const a = +m[2]
    const b = m[3] ? +m[3] : a
    refs.push({ path, startLine: Math.min(a, b), endLine: Math.max(a, b) })
  }
  return refs.length ? refs : null
}
