import { cleanup, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, expect, it, vi } from 'vitest'
import { getHealth } from '../../../../api/repo/repos'
import type { Health, Repo } from '../../../../types/repo/repo'
import { useRepo } from '../../../useRepo'
import { SearchPage } from '../SearchPage'
import { useChat } from '../useChat'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children }: { children: ReactNode }) => <a href="/">{children}</a>,
  useLocation: () => ({}),
  useNavigate: () => vi.fn(),
}))
vi.mock('../../../Frame', () => ({ Frame: ({ children }: { children: ReactNode }) => children }))
vi.mock('../../../../api/repo/repos', () => ({ getHealth: vi.fn() }))
vi.mock('../../../useRepo', () => ({ useRepo: vi.fn() }))
vi.mock('../useChat', () => ({ useChat: vi.fn() }))

const repo = { id: 'r1', owner: 'acme', name: 'app', permissions: ['repo.chat'] } as unknown as Repo

function open(health: () => Promise<Health>) {
  vi.mocked(getHealth).mockImplementation(health)
  vi.mocked(useRepo).mockReturnValue({ data: repo, reload: vi.fn() } as unknown as ReturnType<typeof useRepo>)
  vi.mocked(useChat).mockReturnValue({
    conv: { id: 'c1', snapshotId: 's1', commitSha: 'abc' },
    turns: [],
    selected: null,
    loadError: '',
    busy: false,
    loading: false,
    ask: vi.fn(),
    select: vi.fn(),
  } as unknown as ReturnType<typeof useChat>)
  render(<SearchPage id="r1" conversationId="c1" />)
}

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

it('disables follow-ups when no chat model is set', async () => {
  open(async () => ({ status: 'ok', chatModel: '', embeddingModel: '' }))
  const box = await screen.findByRole('textbox', { name: '請先設定 DEEPCODE_CHAT_MODEL 才能提問' })
  expect((box as HTMLTextAreaElement).disabled).toBe(true)
})

it('disables follow-ups when the chat model cannot be checked', async () => {
  open(async () => {
    throw new Error('Bad Gateway')
  })
  const box = await screen.findByRole('textbox', { name: '目前無法提問：Bad Gateway' })
  expect((box as HTMLTextAreaElement).disabled).toBe(true)
})

it('takes follow-ups once a chat model is set', async () => {
  open(async () => ({ status: 'ok', chatModel: 'gpt', embeddingModel: '' }))
  const box = screen.getByRole('textbox', { name: '繼續追問' })
  await waitFor(() => expect((box as HTMLTextAreaElement).disabled).toBe(false))
})
