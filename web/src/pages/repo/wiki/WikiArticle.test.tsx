import { act, cleanup, render } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { Repo } from '../../../types/repo/repo'
import { WikiArticle } from './WikiArticle'

vi.mock('../../../components/source/diagram/Mermaid', () => ({ Mermaid: () => null }))

const scrollIntoView = vi.fn()
const page = { slug: 'overview', title: 'Overview', content: '## Intro\n\n## Details', citations: [] }
const props = { page, repo: {} as Repo, writing: false, onToc: vi.fn(), onCite: vi.fn() }

beforeEach(() => {
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => callback(0))
  scrollIntoView.mockClear()
  HTMLElement.prototype.scrollIntoView = scrollIntoView
  window.history.replaceState(null, '', '/#h-details')
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

it('waits for loading fonts before scrolling to the final heading position', async () => {
  let ready!: () => void
  Object.defineProperty(document, 'fonts', {
    configurable: true,
    value: { status: 'loading', ready: new Promise<void>((resolve) => (ready = resolve)) },
  })
  render(<WikiArticle {...props} />)
  expect(scrollIntoView).not.toHaveBeenCalled()
  await act(async () => ready())
  expect(scrollIntoView).toHaveBeenCalledTimes(1)
  Object.defineProperty(document, 'fonts', { configurable: true, value: undefined })
})

it('scrolls to the fragment after assigning heading ids', () => {
  render(<WikiArticle {...props} />)
  expect(scrollIntoView).toHaveBeenCalledTimes(1)
  expect(scrollIntoView.mock.instances[0]).toBe(document.getElementById('h-details'))
})

it('waits for content and does not repeat the scroll on a callback rerender', () => {
  const { rerender } = render(<WikiArticle {...props} page={{ ...page, content: '' }} />)
  expect(scrollIntoView).not.toHaveBeenCalled()
  rerender(<WikiArticle {...props} />)
  expect(scrollIntoView).toHaveBeenCalledTimes(1)
  rerender(<WikiArticle {...props} onToc={vi.fn()} />)
  expect(scrollIntoView).toHaveBeenCalledTimes(1)
})

it.each(['', '#missing', '#%E0%A4%A'])('ignores an absent or unmatched fragment: %s', (hash) => {
  window.history.replaceState(null, '', `/${hash}`)
  render(<WikiArticle {...props} />)
  expect(scrollIntoView).not.toHaveBeenCalled()
})
