import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { listBranches, setBranch } from '../../../api/repo/repos'
import type { Repo } from '../../../types/repo/repo'
import { BranchCard } from '../BranchCard'

vi.mock('../../../api/repo/repos', () => ({ listBranches: vi.fn(), setBranch: vi.fn() }))

const repo = { id: 'r1', trackedBranch: '', defaultBranch: 'main' } as Repo

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

it('shows the saved branch when the sync after a branch switch fails', async () => {
  vi.mocked(listBranches).mockResolvedValue({ branches: ['main', 'dev'], trackedBranch: '', defaultBranch: 'main' })
  vi.mocked(setBranch).mockRejectedValue(Object.assign(new Error('Bad Gateway'), { status: 502 }))
  const reloaded = { ...repo, trackedBranch: 'dev' }
  const onChanged = vi.fn(() => rerender(<BranchCard repo={reloaded} onChanged={onChanged} />))
  const { rerender } = render(<BranchCard repo={repo} onChanged={onChanged} />)
  await screen.findByRole('option', { name: 'dev' })
  fireEvent.change(screen.getByRole('combobox', { name: 'Branch' }), { target: { value: 'dev' } })
  fireEvent.click(screen.getByRole('button', { name: '切換' }))
  expect((await screen.findByRole('alert')).textContent).toBe('Bad Gateway')
  await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1))
  expect(screen.getByText(/目前跟隨/).textContent).toBe('目前跟隨 dev')
  expect(screen.getByRole('alert').textContent).toBe('Bad Gateway')
})
