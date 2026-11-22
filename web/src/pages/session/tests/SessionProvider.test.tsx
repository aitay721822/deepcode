import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { SessionProvider } from '../SessionProvider'
import { useSession } from '../session'

const navigate = vi.hoisted(() => vi.fn())
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
  useLocation: () => ({ pathname: '/', searchStr: '' }),
}))

function Screen() {
  const { user, logout } = useSession()
  return user ? (
    <button type="button" onClick={logout}>
      登出 {user.username}
    </button>
  ) : (
    <h1>登入</h1>
  )
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  navigate.mockClear()
})

function setup(failure?: 'network' | 'server' | 'empty') {
  const fetch = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ username: 'admin', permissions: [] })))
  if (failure === 'network') fetch.mockRejectedValueOnce(new TypeError('Failed to fetch'))
  else if (failure === 'server')
    fetch.mockResolvedValueOnce(new Response(JSON.stringify({ error: '登出失敗，請稍後再試。' }), { status: 500 }))
  else if (failure === 'empty') fetch.mockResolvedValueOnce(new Response(null, { status: 500 }))
  else fetch.mockResolvedValueOnce(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetch)
  render(
    <SessionProvider>
      <Screen />
    </SessionProvider>,
  )
  return fetch
}

it.each(['network', 'server', 'empty'] as const)(
  'keeps the current screen and shows an error on %s logout failure',
  async (failure) => {
    const fetch = setup(failure)
    fireEvent.click(await screen.findByRole('button', { name: '登出 admin' }))
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2))
    expect((await screen.findByRole('alert')).textContent).toContain(
      failure === 'network' ? '無法連線到伺服器' : failure === 'server' ? '登出失敗' : 'HTTP 500',
    )
    expect(screen.queryByRole('heading', { name: '登入' })).toBeNull()
    expect(screen.getByRole('button', { name: '登出 admin' })).toBeTruthy()
    expect(navigate).not.toHaveBeenCalled()
    fetch.mockResolvedValueOnce(new Response(null, { status: 204 }))
    fireEvent.click(screen.getByRole('button', { name: '登出 admin' }))
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/login' }))
    expect(screen.queryByRole('alert')).toBeNull()
  },
)

it('navigates to login after a successful logout', async () => {
  setup()
  fireEvent.click(await screen.findByRole('button', { name: '登出 admin' }))
  await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/login' }))
  expect(screen.queryByRole('alert')).toBeNull()
})
