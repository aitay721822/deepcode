import { cleanup, render } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { decode, encodePath } from '../../../lib/url'
import { parseCitation } from '../../repo/sourceLinks'
import { linkMarkers } from '../citations'
import { Markdown } from '../Markdown'

vi.mock('../diagram/Mermaid', () => ({ Mermaid: () => null }))

const repo = { url: 'https://github.com/owner/repo', provider: 'github' as const }
const sha = 'abcdef1234567'

afterEach(cleanup)

it.each([
  ['src/end).ts', 'src/end%29.ts'],
  ['src/start(.ts', 'src/start%28.ts'],
  ['src/(both).ts', 'src/%28both%29.ts'],
  ['src/plain.ts', 'src/plain.ts'],
  ['src/a*b*.ts', 'src/a*b*.ts'],
  ['src/a_b_.ts', 'src/a_b_.ts'],
  ['src/a&b.ts', 'src/a%26b.ts'],
  ['src/a\\*.ts', 'src/a%5C*.ts'],
])('renders the full destination and literal label for %s', (path, encoded) => {
  const linked = linkMarkers(`[${path}:3-5]`, repo, sha)
  const { container } = render(<Markdown text={linked.text} citations={[]} repo={repo} onCite={vi.fn()} />)
  const anchor = container.querySelector('a')
  expect(anchor?.getAttribute('href')).toBe(`${repo.url}/blob/${sha}/${encoded}#L3-L5`)
  expect(anchor?.textContent).toBe(`${path}:3-5 (github.com)`)
  expect(parseCitation(anchor?.getAttribute('href') ?? undefined, repo)).toEqual({
    path,
    sha,
    startLine: 3,
    endLine: 5,
  })
  expect(decode(encodePath(path))).toBe(path)
})

it.each(['', 'not a citation', '[src/end).ts:nope]', '[src/a&amp;.ts:3-5]', '```\n[src/end).ts:3-5]\n```'])(
  'leaves empty, invalid and fenced text unchanged: %s',
  (text) => {
    expect(linkMarkers(text, repo, sha)).toEqual({ text, citations: [] })
  },
)

it('leaves references unchanged without a commit', () => {
  const text = '[src/end).ts:3-5]'
  expect(linkMarkers(text, repo, '')).toEqual({ text, citations: [] })
})
