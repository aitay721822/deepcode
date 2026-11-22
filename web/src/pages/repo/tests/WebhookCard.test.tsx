import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { getWebhook, setWebhook } from '../../../api/repo/repos'
import type { Repo } from '../../../types/repo/repo'
import { WebhookCard } from '../WebhookCard'

vi.mock('../../../api/repo/repos', () => ({
  getWebhook: vi.fn(async () => ({ hasSecret: false })),
  setWebhook: vi.fn(async () => ({ secret: 'one-time-secret' })),
}))

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.clearAllMocks()
})

const repo = { id: 'repo-id' } as Repo

it('lets keyboard users tab to the named secret before the copy button', async () => {
  const user = userEvent.setup()
  render(<WebhookCard repo={repo} />)
  await user.click(await screen.findByRole('button', { name: '產生密鑰' }))
  await screen.findByRole('button', { name: '複製密鑰' })
  await user.tab()
  expect(document.activeElement).toBe(screen.getByRole('button', { name: '移除' }))
  await user.tab()
  const secret = screen.getByLabelText('密鑰')
  expect(document.activeElement).toBe(secret)
  expect(secret.textContent).toBe('one-time-secret')
  await user.tab()
  expect(document.activeElement).toBe(screen.getByRole('button', { name: '複製密鑰' }))
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
})
