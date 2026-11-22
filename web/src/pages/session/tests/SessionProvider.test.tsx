import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { login } from '../../../api/auth'
import { SessionProvider } from '../SessionProvider'
import { useSession } from '../session'

const { navigate, location } = vi.hoisted(() => ({
  navigate: vi.fn(),
  location: { pathname: '/', searchStr: '' },
}))
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
  useLocation: () => location,
}))

function Screen() {
  const { user, logout, refresh } = useSession()
  return user ? (
    <>
      <button type="button" onClick={logout}>
        登出 {user.username}
      </button>
      <button type="button" onClick={refresh}>
        重新整理工作階段
      </button>
    </>
  ) : (
    <button
      type="button"
      onClick={async () => {
        await login('admin', 'password')
        await refresh()
      }}
    >
      登入
    </button>
  )
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  navigate.mockClear()
})

function setup(failure?: 'network' | 'server' | 'empty', pathname = '/') {
  location.pathname = pathname
  const fetch = vi
    .fn()
    .mockResolvedValueOnce(new Response(JSON.stringify({ id: 'admin', username: 'admin', permissions: [] })))
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
    expect(screen.queryByRole('button', { name: /^登入$/ })).toBeNull()
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

it('keeps the error outside the page flow and lets the user dismiss it', async () => {
  setup('server')
  fireEvent.click(await screen.findByRole('button', { name: '登出 admin' }))
  expect((await screen.findByRole('alert')).classList.contains('fixed')).toBe(true)
  fireEvent.click(screen.getByRole('button', { name: '關閉' }))
  expect(screen.queryByRole('alert')).toBeNull()
  expect(screen.getByRole('button', { name: '登出 admin' })).toBeTruthy()
  expect(navigate).not.toHaveBeenCalled()
})

it('clears a failed logout error after a 401 and reauthentication', async () => {
  const fetch = setup('server', '/login')
  fireEvent.click(await screen.findByRole('button', { name: '登出 admin' }))
  await screen.findByRole('alert')
  fetch.mockResolvedValueOnce(new Response(null, { status: 401 }))
  fireEvent.click(screen.getByRole('button', { name: '重新整理工作階段' }))
  const signIn = await screen.findByRole('button', { name: /^登入$/ })
  expect(screen.queryByRole('alert')).toBeNull()
  fetch.mockResolvedValueOnce(new Response('{}'))
  fetch.mockResolvedValueOnce(new Response(JSON.stringify({ id: 'admin', username: 'admin', permissions: [] })))
  fireEvent.click(signIn)
  await screen.findByRole('button', { name: '登出 admin' })
  expect(screen.queryByRole('alert')).toBeNull()
})

it('clears a failed logout error when the signed-in user changes', async () => {
  const fetch = setup('server')
  fireEvent.click(await screen.findByRole('button', { name: '登出 admin' }))
  await screen.findByRole('alert')
  fetch.mockResolvedValueOnce(new Response(JSON.stringify({ id: 'other', username: 'other', permissions: [] })))
  fireEvent.click(screen.getByRole('button', { name: '重新整理工作階段' }))
  await screen.findByRole('button', { name: '登出 other' })
  expect(screen.queryByRole('alert')).toBeNull()
})
