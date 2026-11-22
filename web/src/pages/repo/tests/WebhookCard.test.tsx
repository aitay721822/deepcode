import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { clearWebhook, getWebhook, setWebhook } from '../../../api/repo/repos'
import type { Repo } from '../../../types/repo/repo'
import { WebhookCard } from '../WebhookCard'

vi.mock('../../../api/repo/repos', () => ({
  getWebhook: vi.fn(async () => ({ hasSecret: false })),
  setWebhook: vi.fn(async () => ({ secret: 'one-time-secret' })),
  clearWebhook: vi.fn(async () => ({})),
}))

afterEach(() => {
  cleanup()
  window.getSelection()?.removeAllRanges()
  vi.restoreAllMocks()
  vi.clearAllMocks()
})

const repo = { id: 'repo-id' } as Repo

it('keeps a generated secret when the initial status arrives after generation', async () => {
  let resolveStatus!: (value: { hasSecret: boolean }) => void
  vi.mocked(getWebhook).mockReturnValueOnce(
    new Promise((resolve) => {
      resolveStatus = resolve
    }),
  )
  render(<WebhookCard repo={repo} />)
  fireEvent.click(screen.getByRole('button', { name: '產生密鑰' }))
  expect((await screen.findByRole('button', { name: '移除' })).textContent).toBe('移除')
  await act(async () => resolveStatus({ hasSecret: false }))
  expect(screen.getByRole('button', { name: '移除' }).textContent).toBe('移除')
})

it('keeps a generated then removed secret cleared when the initial status arrives', async () => {
  let resolveStatus!: (value: { hasSecret: boolean }) => void
  vi.mocked(getWebhook).mockReturnValueOnce(
    new Promise((resolve) => {
      resolveStatus = resolve
    }),
  )
  render(<WebhookCard repo={repo} />)
  fireEvent.click(screen.getByRole('button', { name: '產生密鑰' }))
  fireEvent.click(await screen.findByRole('button', { name: '移除' }))
  await screen.findByRole('button', { name: '產生密鑰' })
  expect(clearWebhook).toHaveBeenCalledWith(repo.id)
  await act(async () => resolveStatus({ hasSecret: false }))
  expect(screen.queryByRole('button', { name: '移除' })).toBeNull()
  expect(screen.getByRole('button', { name: '產生密鑰' }).textContent).toContain('產生密鑰')
  expect(screen.queryByText('one-time-secret')).toBeNull()
  expect(screen.queryByRole('alert')).toBeNull()
})

it('ignores a late status error after generation succeeds', async () => {
  let rejectStatus!: (error: Error) => void
  vi.mocked(getWebhook).mockReturnValueOnce(
    new Promise((_, reject) => {
      rejectStatus = reject
    }),
  )
  render(<WebhookCard repo={repo} />)
  fireEvent.click(screen.getByRole('button', { name: '產生密鑰' }))
  await screen.findByRole('button', { name: '移除' })
  await act(async () => rejectStatus(new Error('stale read failed')))
  expect(screen.queryByRole('alert')).toBeNull()
})

it.each([false, true])('shows the initial status without a write: %s', async (hasSecret) => {
  vi.mocked(getWebhook).mockResolvedValueOnce({ hasSecret })
  render(<WebhookCard repo={repo} />)
  await act(async () => {})
  expect(screen.queryByRole('button', { name: '移除' })?.textContent ?? '').toBe(hasSecret ? '移除' : '')
})

it('shows an initial status error without a write', async () => {
  vi.mocked(getWebhook).mockRejectedValueOnce(new Error('status failed'))
  render(<WebhookCard repo={repo} />)
  expect((await screen.findByRole('alert')).textContent).toBe('status failed')
})

it('still applies the initial status after generation fails', async () => {
  let resolveStatus!: (value: { hasSecret: boolean }) => void
  vi.mocked(getWebhook).mockReturnValueOnce(
    new Promise((resolve) => {
      resolveStatus = resolve
    }),
  )
  vi.mocked(setWebhook).mockRejectedValueOnce(new Error('generation failed'))
  render(<WebhookCard repo={repo} />)
  fireEvent.click(screen.getByRole('button', { name: '產生密鑰' }))
  expect((await screen.findByRole('alert')).textContent).toBe('generation failed')
  await act(async () => resolveStatus({ hasSecret: true }))
  expect(screen.getByRole('button', { name: '移除' }).textContent).toBe('移除')
})

it('does not offer to copy a secret before generating it', async () => {
  render(<WebhookCard repo={repo} />)
  await waitFor(() => expect(getWebhook).toHaveBeenCalledWith(repo.id))
  expect(screen.queryByRole('button', { name: '複製密鑰' })).toBeNull()
})

it.each([false, true])('shows copy feedback when clipboard access is denied: %s', async (denied) => {
  const writeText = vi.fn(() => {
    const result = denied ? Promise.reject(new DOMException('Permission denied', 'NotAllowedError')) : Promise.resolve()
    void result.catch(() => {})
    return result
  })
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
  render(<WebhookCard repo={repo} />)
  fireEvent.click(await screen.findByRole('button', { name: '產生密鑰' }))
  fireEvent.click(await screen.findByRole('button', { name: '複製密鑰' }))
  expect(setWebhook).toHaveBeenCalledWith(repo.id)
  expect(writeText).toHaveBeenCalledWith('one-time-secret')
  await waitFor(() => expect(screen.getByRole('status').textContent).toBe(denied ? '複製失敗' : '已複製'))
  const button = screen.getByRole('button', { name: '複製密鑰' })
  expect(button.querySelector(denied ? '.lucide-x' : '.lucide-check')).not.toBeNull()
  expect(window.getSelection()?.toString()).toBe(denied ? 'one-time-secret' : '')
})
