import {
  createBrowserHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
  useLocation,
} from '@tanstack/react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { useState } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { Repo } from '../../../../types/repo/repo'
import type { TocItem } from '../Toc'
import { Toc } from '../Toc'
import { WikiArticle } from '../WikiArticle'

vi.mock('../../../../components/source/diagram/Mermaid', () => ({ Mermaid: () => null }))

const page = { slug: 'overview', title: 'Overview', content: '## Details', citations: [] }

beforeEach(() => {
  vi.stubGlobal('scrollTo', vi.fn())
})

function Page() {
  const hash = useLocation({ select: (location) => location.hash })
  const [items, setItems] = useState<TocItem[]>([])
  return (
    <>
      <output>{hash}</output>
      <Toc items={items} />
      <WikiArticle page={page} repo={{} as Repo} writing={false} onToc={setItems} onCite={vi.fn()} />
    </>
  )
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

it.each([false, true])('updates browser and router hash, scrolls and focuses (reduced motion: %s)', async (reduced) => {
  window.history.replaceState(null, '', '/?page=overview')
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => callback(0))
  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => ({ matches: reduced })),
  )
  const scroll = vi.fn()
  const focus = vi.spyOn(HTMLElement.prototype, 'focus')
  HTMLElement.prototype.scrollIntoView = scroll
  const router = createRouter({ routeTree: createRootRoute({ component: Page }), history: createBrowserHistory() })
  const order: string[] = []
  router.subscribe('onBeforeLoad', () => order.push('navigation'))
  scroll.mockImplementation(() => order.push('scroll'))
  render(<RouterProvider router={router} />)
  const link = await screen.findByRole('link', { name: 'Details' })
  order.length = 0
  fireEvent.click(link)
  await waitFor(() => expect(window.location.hash).toBe('#h-details'))
  expect(screen.getByRole('status').textContent).toBe('h-details')
  expect(document.activeElement).toBe(screen.getByRole('heading', { name: 'Details' }))
  expect(focus).toHaveBeenCalledWith({ preventScroll: true })
  expect(window.location.search).toBe('?page=overview')
  expect(scroll).toHaveBeenCalledExactlyOnceWith({ behavior: reduced ? 'auto' : 'smooth', block: 'start' })
  expect(order).toEqual(['navigation', 'scroll'])
  router.history.back()
  await waitFor(() => expect(window.location.hash).toBe(''))
  await waitFor(() => expect(screen.getByRole('status').textContent).toBe(''))
})

it.each(['metaKey', 'ctrlKey', 'shiftKey', 'altKey'])('leaves %s clicks to the browser', async (modifier) => {
  window.history.replaceState(null, '', '/')
  const scroll = vi.fn()
  HTMLElement.prototype.scrollIntoView = scroll
  const router = createRouter({ routeTree: createRootRoute({ component: Page }), history: createBrowserHistory() })
  render(<RouterProvider router={router} />)
  const link = await screen.findByRole('link', { name: 'Details' })
  const event = new MouseEvent('click', { bubbles: true, cancelable: true, [modifier]: true })
  link.dispatchEvent(event)
  expect(event.defaultPrevented).toBe(false)
  expect(router.state.location.hash).toBe('')
  expect(scroll).not.toHaveBeenCalled()
  event.preventDefault()
})

function MissingHeading() {
  return <Toc items={[{ id: 'missing', text: 'Missing', level: 3 }]} />
}

it('still navigates when the heading is absent', async () => {
  window.history.replaceState(null, '', '/')
  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => ({ matches: false })),
  )
  const router = createRouter({
    routeTree: createRootRoute({ component: MissingHeading }),
    history: createBrowserHistory(),
  })
  render(<RouterProvider router={router} />)
  fireEvent.click(await screen.findByRole('link', { name: 'Missing' }))
  await waitFor(() => expect(window.location.hash).toBe('#missing'))
})

it('renders an empty outline without links', () => {
  render(<Toc items={[]} />)
  expect(screen.queryByRole('link')).toBeNull()
})
