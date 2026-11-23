import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeAll, expect, it, vi } from 'vitest'
import { getJob, listJobs } from '../../../../api/admin/jobs'
import { routeTree } from '../../../../routeTree.gen'
import type { JobRow } from '../../../../types/admin/jobs'
import { SessionContext } from '../../../session/session'

vi.mock('../../../../api/admin/jobs', () => ({
  listJobs: vi.fn(),
  getJob: vi.fn(),
  watchJobs: () => () => {},
  watchJobOutput: () => () => {},
}))
vi.mock('../../../Frame', () => ({ Frame: ({ children }: { children: ReactNode }) => children }))
vi.mock('../../../session/SessionProvider', () => ({
  SessionProvider: ({ children }: { children: ReactNode }) => (
    <SessionContext.Provider value={{ user: null, can: () => true, logout: vi.fn(), refresh: vi.fn() }}>
      {children}
    </SessionContext.Provider>
  ),
}))

const job = (id: string, repoName: string, status: JobRow['status']) =>
  ({
    id,
    kind: 'index',
    status,
    repoId: 'r',
    repoOwner: 'acme',
    repoName,
    stage: '',
    progress: 0,
    total: 0,
    error: '',
    attempts: 1,
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
  }) as JobRow
const done = job('a00000000000000000000001', 'done', 'succeeded')
const broken = job('a00000000000000000000002', 'broken', 'failed')

beforeAll(() => {
  // What Radix Select reaches for, which jsdom lacks.
  Element.prototype.hasPointerCapture = () => false
  Element.prototype.releasePointerCapture = () => {}
  Element.prototype.scrollIntoView = () => {}
})

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

it('keeps the status filter through a job and back, by its link or the browser', async () => {
  vi.mocked(listJobs).mockImplementation(async (status) => [done, broken].filter((j) => !status || j.status === status))
  vi.mocked(getJob).mockResolvedValue({ job: done, logs: [] })
  const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: ['/admin/jobs'] }) })
  render(<RouterProvider router={router} />)
  await screen.findByText('acme/broken')

  const filter = screen.getByRole('combobox', { name: '狀態' })
  fireEvent.pointerDown(filter, { button: 0, pointerType: 'mouse' })
  fireEvent.click(await screen.findByRole('option', { name: '完成' }))
  await act(async () => {}) // the filtered list loads
  expect(screen.queryByText('acme/broken')).toBeNull()

  fireEvent.click(screen.getByRole('link', { name: 'acme/done' }))
  await screen.findByRole('heading', { name: 'acme/done' })
  fireEvent.click(within(screen.getByRole('main')).getByRole('link', { name: '工作' }))
  await screen.findByRole('link', { name: 'acme/done' })
  await act(async () => {})
  expect(screen.getByRole('combobox', { name: '狀態' }).textContent).toBe('完成')
  expect(screen.queryByText('acme/broken')).toBeNull()
  expect(router.state.location.search).toEqual({ status: 'succeeded' })

  fireEvent.click(screen.getByRole('link', { name: 'acme/done' }))
  await screen.findByRole('heading', { name: 'acme/done' })
  await act(async () => router.history.back()) // the browser's Back, not the page's link
  await screen.findByRole('link', { name: 'acme/done' })
  await act(async () => {})
  expect(screen.getByRole('combobox', { name: '狀態' }).textContent).toBe('完成')
  expect(screen.queryByText('acme/broken')).toBeNull()
})
