import { cleanup, render, screen, waitFor } from '@testing-library/react'
import type { ComponentProps } from 'react'
import { afterEach, expect, it, vi } from 'vitest'
import { AdminPage } from '../../AdminPage'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, ref, 'aria-current': current }: ComponentProps<'a'>) => (
    <a href="/admin" aria-current={current} ref={ref}>
      {children}
    </a>
  ),
  useNavigate: () => vi.fn(),
}))
vi.mock('../../../Frame', () => ({ Frame: ({ children }: ComponentProps<'div'>) => children }))
vi.mock('../../../session/session', () => ({ useSession: () => ({ can: () => true }) }))
vi.mock('../ServersSection', () => ({ ServersSection: () => null }))
vi.mock('../../overview/ProjectsSection', () => ({ ProjectsSection: () => null }))

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

it('reveals the active pill on a mobile deep link and when switching sections', async () => {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    if (this.tagName === 'NAV') return new DOMRect(0, 0, 390, 56)
    const railScroll = this.parentElement?.scrollLeft ?? 0
    if (this.textContent?.includes('Git 伺服器')) return new DOMRect(420 - railScroll, 0, 110, 32)
    return new DOMRect(16 - railScroll, 0, 70, 32)
  })

  const { rerender } = render(<AdminPage section="servers" />)
  const rail = screen.getByRole('navigation', { name: '管理後台' })
  expect(screen.getByRole('link', { name: 'Git 伺服器', current: 'page' })).toBeTruthy()
  await waitFor(() => expect(rail.scrollLeft).toBe(140))

  rerender(<AdminPage section="projects" />)
  expect(screen.getByRole('link', { name: '專案', current: 'page' })).toBeTruthy()
  await waitFor(() => expect(rail.scrollLeft).toBe(16))
})
