import { Link } from '@tanstack/react-router'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import type { ComponentProps, ReactNode } from 'react'
import { afterEach, expect, it, vi } from 'vitest'
import { getWiki } from '../../../api/repo/wiki'
import type { Job, Repo } from '../../../types/repo/repo'
import { useRepo } from '../../useRepo'
import { RepoPage } from '../RepoPage'

vi.mock('@tanstack/react-router', () => ({
  Link: vi.fn(
    ({
      children,
      params,
      search,
      activeOptions: _activeOptions,
      ...props
    }: ComponentProps<'a'> & {
      params: { id: string }
      search?: { page?: string }
      activeOptions?: { exact: boolean }
    }) => (
      <a {...props} href={`/repos/${params.id}${search?.page ? `?page=${search.page}` : ''}`}>
        {children}
      </a>
    ),
  ),
  useNavigate: () => vi.fn(),
}))
vi.mock('../../Frame', () => ({ Frame: ({ children }: { children: ReactNode }) => children }))
vi.mock('../../useRepo', () => ({ useRepo: vi.fn() }))
vi.mock('../../../api/repo/wiki', () => ({ getWiki: vi.fn() }))
vi.mock('../search/useChatModel', () => ({ useChatModel: () => ({}) }))
vi.mock('../wiki/WikiArticle', () => ({ WikiArticle: () => <article>Overview article</article> }))

const pages = [{ slug: 'overview', title: 'Overview', content: '# Overview', citations: [] }]

function open(search: { page?: string }, wikiPages = pages, job?: Job) {
  vi.mocked(useRepo).mockReturnValue({
    data: {
      id: 'r1',
      permissions: [],
      job,
      snapshot: { id: 's1', commitSha: 'abc', indexedAt: '2026-01-01' },
    } as unknown as Repo,
    error: undefined,
    status: undefined,
    reload: vi.fn(),
  })
  vi.mocked(getWiki).mockResolvedValue({
    snapshotId: 's1',
    commitSha: 'abc',
    indexedAt: '',
    status: 'ready',
    pages: wikiPages,
  })
  return render(<RepoPage id="r1" search={search} />)
}

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

it('keeps the article visible when a wiki poll fails and retries the fetch', async () => {
  open({}, pages, { kind: 'wiki', status: 'running' } as Job)
  expect((await screen.findByText('Overview article')).textContent).toBe('Overview article')
  vi.mocked(getWiki).mockRejectedValueOnce(Object.assign(new Error('Internal Server Error'), { status: 500 }))
  expect((await screen.findByRole('alert', {}, { timeout: 4000 })).textContent).toBe('Internal Server Error')
  expect(screen.getByText('Overview article').textContent).toBe('Overview article')
  expect(
    screen.getByRole('alert').compareDocumentPosition(screen.getByRole('article')) & Node.DOCUMENT_POSITION_FOLLOWING,
  ).toBeTruthy()
  await act(async () => fireEvent.click(screen.getByRole('button', { name: '重試' })))
  expect(getWiki).toHaveBeenCalledTimes(3)
  expect(screen.queryByRole('alert')).toBeNull()
  expect(screen.getByText('Overview article').textContent).toBe('Overview article')
}, 6000)

it('shows a missing page and Overview link without highlighting a wiki page', async () => {
  const { container } = open({ page: 'does-not-exist' })
  expect((await screen.findByRole('heading', { name: '找不到 Wiki 頁面' })).textContent).toBe('找不到 Wiki 頁面')
  expect(screen.getByText('找不到頁面「does-not-exist」。').textContent).toContain('does-not-exist')
  expect(screen.getByRole('link', { name: '返回 Overview' }).getAttribute('href')).toBe('/repos/r1')
  expect(Link).toHaveBeenCalledWith(
    expect.objectContaining({ children: '返回 Overview', activeOptions: { exact: true } }),
    undefined,
  )
  expect(container.querySelector('[aria-current="page"]')).toBeNull()
  expect(screen.queryByText('Overview article')).toBeNull()
})

it('shows and highlights Overview when no page is requested', async () => {
  open({})
  expect((await screen.findByText('Overview article')).textContent).toBe('Overview article')
  expect(screen.getByRole('link', { name: /Overview/ }).getAttribute('aria-current')).toBe('page')
})

it('does not call an existing slug missing', async () => {
  open({ page: 'overview' })
  expect((await screen.findByText('Overview article')).textContent).toBe('Overview article')
  expect(screen.queryByText('找不到 Wiki 頁面')).toBeNull()
})

it('shows a missing page when the loaded wiki is empty', async () => {
  open({ page: 'does-not-exist' }, [])
  expect((await screen.findByRole('heading', { name: '找不到 Wiki 頁面' })).textContent).toBe('找不到 Wiki 頁面')
})

it.each(['queued', 'running'] as const)(
  'keeps the job card for a missing slug while the wiki job is %s',
  async (status) => {
    open({ page: 'coming-soon' }, [], {
      id: 'j1',
      kind: 'wiki',
      status,
      stage: 'plan',
      commitSha: 'abc',
      attempts: 0,
      retries: 0,
      progress: 0,
      total: 0,
      error: '',
      runAfter: '',
      createdAt: '',
      updatedAt: '',
    })
    await act(async () => {
      await vi.mocked(getWiki).mock.results[0].value
    })
    expect(screen.getByRole('status').textContent).toContain('產生 Wiki')
    expect(screen.queryByText('找不到 Wiki 頁面')).toBeNull()
    expect(screen.queryByRole('link', { name: '返回 Overview' })).toBeNull()
  },
)

it('shows the load error rather than claiming the page is missing', async () => {
  vi.mocked(getWiki).mockRejectedValueOnce(new Error('Bad Gateway'))
  open({ page: 'does-not-exist' })
  expect((await screen.findByRole('alert')).textContent).toBe('Bad Gateway')
  expect(screen.queryByText('找不到 Wiki 頁面')).toBeNull()
})
