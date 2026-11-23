import { Link } from '@tanstack/react-router'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import type { ComponentProps, ReactNode } from 'react'
import { afterEach, expect, it, vi } from 'vitest'
import { getFile, listFiles } from '../../../api/repo/code'
import { getWiki } from '../../../api/repo/wiki'
import type { FileContent } from '../../../types/repo/code'
import type { Job, Repo } from '../../../types/repo/repo'
import type { Wiki } from '../../../types/repo/wiki'
import { useRepo } from '../../useRepo'
import { RepoPage } from '../RepoPage'

const { navigate } = vi.hoisted(() => ({
  navigate: vi.fn<(options: { search?: ComponentProps<typeof RepoPage>['search'] }) => void>(),
}))

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
  useNavigate: () => navigate,
}))
vi.mock('../../Frame', () => ({ Frame: ({ children }: { children: ReactNode }) => children }))
vi.mock('../../useRepo', () => ({ useRepo: vi.fn() }))
vi.mock('../../../api/repo/code', () => ({ getFile: vi.fn(), listFiles: vi.fn() }))
vi.mock('../../../api/repo/wiki', () => ({ getWiki: vi.fn() }))
vi.mock('../search/useChatModel', () => ({ useChatModel: () => ({ ready: true }) }))
vi.mock('../wiki/WikiArticle', () => ({
  WikiArticle: ({ page }: { page: Wiki['pages'][number] }) => <article>{page.title} article</article>,
}))

const pages = [{ slug: 'overview', title: 'Overview', content: '# Overview', citations: [] }]

function setRepo(job?: Job, permissions: Repo['permissions'] = []) {
  vi.mocked(useRepo).mockReturnValue({
    data: {
      id: 'r1',
      url: 'https://github.com/acme/demo',
      permissions,
      job,
      snapshot: { id: 's1', commitSha: 'abc', indexedAt: '2026-01-01' },
    } as unknown as Repo,
    error: undefined,
    status: undefined,
    loading: false,
    reload: vi.fn(),
  })
}

function open(search: { page?: string }, wikiPages = pages, job?: Job, permissions?: Repo['permissions']) {
  setRepo(job, permissions)
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
  navigate.mockReset()
  vi.unstubAllGlobals()
})

function mockDesktop(desktop: boolean) {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: desktop && query === '(min-width: 900px)',
    addEventListener: vi.fn(),
    addListener: vi.fn(),
  }))
}

it.each([
  { desktop: false, typing: false },
  { desktop: true, typing: false },
  { desktop: false, typing: true },
])('reveals a selected file after it renders (desktop: $desktop, typing: $typing)', async ({ desktop, typing }) => {
  mockDesktop(desktop)
  vi.mocked(listFiles).mockResolvedValue([{ path: 'main.go', language: 'go', lines: 1, size: 12 }])
  let resolveFile!: (file: FileContent) => void
  vi.mocked(getFile).mockReturnValueOnce(new Promise((resolve) => (resolveFile = resolve)))
  const { rerender } = open({ page: 'overview' }, pages, undefined, typing ? ['repo.chat'] : [])
  navigate.mockImplementation(({ search = {} }) => rerender(<RepoPage id="r1" search={search} />))
  await screen.findByText('Overview article')

  const main = screen.getByRole('main')
  const scrollIntoView = vi.fn(() => main.querySelector('code')?.textContent)
  main.scrollIntoView = scrollIntoView
  const focus = vi.spyOn(main, 'focus')

  fireEvent.mouseDown(screen.getByRole('tab', { name: '檔案' }), { button: 0 })
  const file = await screen.findByRole('button', { name: 'main.go' })
  file.focus()
  await act(async () => fireEvent.click(file))

  expect(navigate).toHaveBeenCalledWith(
    expect.objectContaining({ search: { page: 'overview', file: 'main.go', L: undefined }, resetScroll: desktop }),
  )
  expect(screen.getByText('Overview article')).toBeTruthy()
  expect.soft(scrollIntoView).not.toHaveBeenCalled()
  expect.soft(focus).not.toHaveBeenCalled()
  expect.soft(main.hasAttribute('inert')).toBe(!desktop)
  expect.soft(document.activeElement).toBe(file)

  await screen.findByRole('heading', { name: 'main.go' })
  expect(screen.queryByText('Overview article')).toBeNull()
  expect.soft(scrollIntoView).not.toHaveBeenCalled()
  expect.soft(focus).not.toHaveBeenCalled()

  const question = typing ? screen.getByRole('textbox', { name: /提問/ }) : null
  if (question) {
    question.focus()
    expect(document.activeElement).toBe(question)
  }
  await act(async () => resolveFile({ path: 'main.go', content: 'package main', language: 'go', commitSha: 'abc' }))
  expect(main.querySelector('code')?.textContent).toBe('package main')
  expect(main.hasAttribute('inert')).toBe(false)
  if (desktop) {
    expect(scrollIntoView).not.toHaveBeenCalled()
    expect(document.activeElement).toBe(file)
  } else {
    expect(scrollIntoView).toHaveBeenCalledWith({ block: 'start' })
    expect(scrollIntoView).toHaveReturnedWith('package main')
    if (question) {
      expect(document.activeElement).toBe(question)
      expect(focus).not.toHaveBeenCalled()
    } else {
      expect(focus).toHaveBeenCalledWith({ preventScroll: true })
      expect(document.activeElement).toBe(main)
    }
  }
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

it('returns to the selected wiki article from an open file', async () => {
  mockDesktop(true)
  const article = { ...pages[0], slug: '2-server', title: 'Server' }
  vi.mocked(listFiles).mockResolvedValue([{ path: 'main.go', language: 'go', size: 12, lines: 1 }])
  vi.mocked(getFile).mockResolvedValue({ path: 'main.go', content: 'package main', language: 'go', commitSha: 'abc' })
  const { rerender } = open({ page: article.slug }, [...pages, article])
  navigate.mockImplementation(({ search = {} }) => rerender(<RepoPage id="r1" search={search} />))
  expect(await screen.findByText('Server article')).toBeTruthy()

  fireEvent.mouseDown(screen.getByRole('tab', { name: '檔案' }), { button: 0 })
  fireEvent.click(await screen.findByRole('button', { name: 'main.go' }))

  expect(await screen.findByRole('heading', { name: 'main.go' })).toBeTruthy()
  fireEvent.mouseDown(screen.getByRole('tab', { name: 'Wiki' }), { button: 0 })

  expect(await screen.findByText('Server article')).toBeTruthy()
  expect(screen.getByRole('link', { name: 'Server' }).getAttribute('aria-current')).toBe('page')
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
    const observer = new MutationObserver(() => {})
    observer.observe(screen.getByRole('main'), { childList: true, subtree: true })
    rerender(<RepoPage id="r1" search={search} />)
    const addedText = observer
      .takeRecords()
      .flatMap(({ addedNodes }) => Array.from(addedNodes, (node) => node.textContent))
    observer.disconnect()

    expect(getWiki).toHaveBeenCalledTimes(2)
    expect(addedText.filter((text) => text?.includes('找不到 Wiki 頁面'))).toEqual([])
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
