import { Link } from '@tanstack/react-router'
import { act, cleanup, render, screen } from '@testing-library/react'
import type { ComponentProps, ReactNode } from 'react'
import { afterEach, expect, it, vi } from 'vitest'
import { getWiki } from '../../../api/repo/wiki'
import type { Job, Repo } from '../../../types/repo/repo'
import type { Wiki } from '../../../types/repo/wiki'
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

function setRepo(job?: Job) {
  vi.mocked(useRepo).mockReturnValue({
    data: {
      id: 'r1',
      permissions: [],
      job,
      snapshot: { id: 's1', commitSha: 'abc', indexedAt: '2026-01-01' },
    } as unknown as Repo,
    error: undefined,
    status: undefined,
    loading: false,
    reload: vi.fn(),
  })
}

function open(search: { page?: string }, wikiPages = pages, job?: Job) {
  setRepo(job)
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

it.each(['found', 'missing', 'failed'] as const)(
  'waits for the completion refetch before judging the slug (%s)',
  async (outcome) => {
    const job: Job = {
      id: 'j1',
      kind: 'wiki',
      status: 'running',
      stage: 'write',
      commitSha: 'abc',
      attempts: 0,
      retries: 0,
      progress: 1,
      total: 2,
      error: '',
      runAfter: '',
      createdAt: '',
      updatedAt: '',
    }
    const search = { page: '2-server' }
    const { rerender } = open(search, pages, job)
    await screen.findByRole('link', { name: /Overview/ })
    expect(screen.getByRole('status').textContent).toContain('產生 Wiki')

    let resolveRefetch!: (wiki: Wiki) => void
    let rejectRefetch!: (error: Error) => void
    vi.mocked(getWiki).mockReturnValueOnce(
      new Promise((resolve, reject) => {
        resolveRefetch = resolve
        rejectRefetch = reject
      }),
    )
    setRepo({ ...job, status: 'succeeded' })
    rerender(<RepoPage id="r1" search={search} />)

    expect(getWiki).toHaveBeenCalledTimes(2)
    expect(screen.queryByRole('heading', { name: '找不到 Wiki 頁面' })).toBeNull()
    expect(screen.getByRole('status', { name: '載入中' })).toBeTruthy()
    expect(screen.getByRole('link', { name: /Overview/ })).toBeTruthy()

    if (outcome === 'failed') {
      rejectRefetch(new Error('Bad Gateway'))
      expect((await screen.findByRole('alert')).textContent).toBe('Bad Gateway')
      expect(screen.queryByRole('heading', { name: '找不到 Wiki 頁面' })).toBeNull()
    } else {
      resolveRefetch({
        snapshotId: 's1',
        commitSha: 'abc',
        indexedAt: '',
        status: 'ready',
        pages: outcome === 'found' ? [...pages, { ...pages[0], slug: search.page, title: 'Server' }] : pages,
      })
      if (outcome === 'found') {
        expect(await screen.findByRole('article')).toBeTruthy()
        expect(screen.getByRole('link', { name: /Server/ }).getAttribute('aria-current')).toBe('page')
      } else {
        expect(await screen.findByRole('heading', { name: '找不到 Wiki 頁面' })).toBeTruthy()
      }
    }
  },
)
