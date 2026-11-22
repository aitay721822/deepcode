import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { getMe } from '../../../api/auth'
import { useApi } from '../../../lib/useApi'
import type { Me } from '../../../types/auth'
import { SessionProvider } from '../SessionProvider'

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
  useLocation: () => ({ pathname: '/repo/private', searchStr: '' }),
}))
vi.mock('../../../api/auth', () => ({ getMe: vi.fn(), logout: vi.fn() }))
const navigate = vi.fn()
const admin: Me = { id: 'admin', username: 'admin', displayName: 'Admin', permissions: [], allRepositories: true }
const member: Me = { ...admin, id: 'member', username: 'member' }
function Wiki({ load }: { load: () => Promise<string> }) {
  const { data, error } = useApi('wiki/private', load)
  return <div>{data ?? error ?? 'Loading wiki'}</div>
}
afterEach(() => {
  cleanup()
  vi.resetAllMocks()
})

it.each(['Member wiki', '', 'Not found'])('discards private API data when the new account loads %j', async (result) => {
  vi.mocked(getMe).mockResolvedValue(admin)
  const load = vi.fn().mockResolvedValue('Private admin wiki / Sync')
  render(
    <SessionProvider>
      <Wiki load={load} />
    </SessionProvider>,
  )
  expect(await screen.findByText('Private admin wiki / Sync')).toBeTruthy()
  fireEvent(document, new Event('visibilitychange'))
  await waitFor(() => expect(getMe).toHaveBeenCalledTimes(2))
  expect(load).toHaveBeenCalledTimes(1)
  vi.mocked(getMe).mockRejectedValueOnce(new Error('Offline'))
  fireEvent(document, new Event('visibilitychange'))
  await waitFor(() => expect(getMe).toHaveBeenCalledTimes(3))
  expect(screen.getByText('Private admin wiki / Sync')).toBeTruthy()
  vi.mocked(getMe).mockResolvedValue(member)
  let resolve!: (value: string) => void
  let reject!: (error: Error) => void
  load.mockReturnValueOnce(
    new Promise<string>((yes, no) => {
      resolve = yes
      reject = no
    }),
  )
  fireEvent(document, new Event('visibilitychange'))
  expect(await screen.findByText('Loading wiki')).toBeTruthy()
  expect(screen.queryByText('Private admin wiki / Sync')).toBeNull()
  await waitFor(() => expect(load).toHaveBeenCalledTimes(2))
  await act(async () => {
    if (result === 'Not found') reject(new Error(result))
    else resolve(result)
  })
  if (result) expect(await screen.findByText(result)).toBeTruthy()
  expect(screen.queryByText('Private admin wiki / Sync')).toBeNull()
  expect(load).toHaveBeenCalledTimes(2)
})

it('removes private data on cross-tab sign-out', async () => {
  vi.mocked(getMe).mockResolvedValue(admin)
  render(
    <SessionProvider>
      <div>Private wiki</div>
    </SessionProvider>,
  )
  await screen.findByText('Private wiki')
  vi.mocked(getMe).mockRejectedValue({ status: 401 })
  fireEvent(document, new Event('visibilitychange'))
  await waitFor(() => expect(screen.queryByText('Private wiki')).toBeNull())
})
