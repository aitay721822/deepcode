import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { WikiNav } from '../WikiNav'

vi.mock('@tanstack/react-router', () => ({ Link: () => null }))

const repoId = 'demo-id'
const pages = [{ slug: 'overview', title: 'Overview', content: '# Overview', citations: [] }]

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

it.each([
  ['attachment; filename="acme-demo-wiki-389d686.zip"', 'acme-demo-wiki-389d686.zip'],
  [null, 'wiki-demo-id.zip'],
])('uses the download filename for Content-Disposition %s', async (disposition, filename) => {
  const headers = disposition ? { 'Content-Disposition': disposition } : undefined
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('zip bytes', { headers })))
  vi.stubGlobal(
    'URL',
    class extends URL {
      static createObjectURL = () => 'blob:wiki'
      static revokeObjectURL = vi.fn()
    },
  )
  const filenames: string[] = []
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
    filenames.push(this.download)
  })

  render(<WikiNav pages={pages} repoId={repoId} writing={false} />)
  fireEvent.click(screen.getByRole('button', { name: '匯出 Markdown' }))

  await waitFor(() => expect(filenames).toEqual([filename]))
})

it('shows the server error instead of starting a download', async () => {
  const message = '這個儲存庫還沒有 wiki'
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: message }), { status: 404 })))
  const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

  render(<WikiNav pages={pages} repoId={repoId} writing={false} />)
  fireEvent.click(screen.getByRole('button', { name: '匯出 Markdown' }))

  expect((await screen.findByRole('alert')).textContent).toBe(message)
  expect(click).not.toHaveBeenCalled()
})
