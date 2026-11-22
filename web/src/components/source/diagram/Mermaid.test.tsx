import { cleanup, fireEvent, render, screen, waitFor, waitForElementToBeRemoved, within } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { Mermaid } from './Mermaid'

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn(async (_id: string, code: string) => {
      if (code === 'invalid') throw new Error('Invalid diagram')
      if (code === 'empty') return { svg: '<svg></svg>' }
      return {
        svg: '<svg><g class="node" id="flowchart-source-0"><text>Source file</text></g><text>Background</text></svg>',
      }
    }),
  },
}))

afterEach(cleanup)

it.each(['click', 'Enter', ' '])('opens source with %s inline, fullscreen and after closing', async (action) => {
  const onNode = vi.fn()
  render(<Mermaid code="graph TD; source[Source file]" onNode={onNode} />)
  const activate = async (scope: HTMLElement) => {
    const node = await within(scope).findByText('Source file')
    if (action === 'click') fireEvent.click(node)
    else fireEvent.keyDown(node, { key: action })
  }
  await activate(document.body)
  expect(onNode).toHaveBeenCalledWith('source')
  expect(screen.queryByRole('dialog')).toBeNull()
  onNode.mockClear()
  fireEvent.click(screen.getByRole('button', { name: /全/ }))
  const dialog = await screen.findByRole('dialog')
  await activate(dialog)
  await waitFor(() => expect(dialog.getAttribute('data-state')).toBe('closed'))
  expect(onNode).toHaveBeenCalledWith('source')
  await waitForElementToBeRemoved(dialog)
  onNode.mockClear()
  await activate(screen.getByRole('group'))
  expect(onNode).toHaveBeenCalledWith('source')
})

it('lets a focused fullscreen node open source with Enter', async () => {
  const onNode = vi.fn()
  render(<Mermaid code="graph TD; source" onNode={onNode} />)
  fireEvent.click(await screen.findByRole('button', { name: /全/ }))
  const dialog = await screen.findByRole('dialog')
  const node = within(dialog).getByRole('button', { name: 'Source file' })
  expect(node.getAttribute('tabindex')).toBe('0')
  node.focus()
  expect(document.activeElement).toBe(node)
  fireEvent.keyDown(node, { key: 'Enter' })
  await waitForElementToBeRemoved(dialog)
  expect(onNode).toHaveBeenCalledWith('source')
})

it.each(['a node', 'Escape'])('returns focus to the fullscreen button when %s closes the dialog', async (how) => {
  render(<Mermaid code="graph TD; source" onNode={vi.fn()} />)
  const expand = await screen.findByRole('button', { name: /全/ })
  expand.focus()
  const focus = vi.spyOn(expand, 'focus')
  fireEvent.click(expand)
  const dialog = await screen.findByRole('dialog')
  const node = within(dialog).getByRole('button', { name: 'Source file' })
  node.focus()
  fireEvent.keyDown(node, { key: how === 'a node' ? 'Enter' : 'Escape' })
  await waitForElementToBeRemoved(dialog)
  await waitFor(() => expect(document.activeElement).toBe(expand))
  expect(focus).toHaveBeenLastCalledWith({ preventScroll: true }) // the source a node opened may be scrolled to
})

it('ignores the background and leaves diagrams without a callback noninteractive', async () => {
  const onNode = vi.fn()
  const { rerender } = render(<Mermaid code="graph TD; source" onNode={onNode} />)
  fireEvent.click(await screen.findByText('Background'))
  expect(onNode).not.toHaveBeenCalled()
  rerender(<Mermaid code="graph TD; source" />)
  fireEvent.click(screen.getByRole('button', { name: /全/ }))
  const dialog = await screen.findByRole('dialog')
  expect(within(dialog).queryByRole('button', { name: 'Source file' })).toBeNull()
})

it('shows the code when rendering fails', async () => {
  render(<Mermaid code="invalid" onNode={vi.fn()} />)
  expect(await screen.findByText('invalid')).toBeTruthy()
  expect(screen.queryByRole('button')).toBeNull()
})

it('handles a diagram with no nodes', async () => {
  const onNode = vi.fn()
  render(<Mermaid code="empty" onNode={onNode} />)
  fireEvent.click(await screen.findByRole('button', { name: /全/ }))
  const dialog = await screen.findByRole('dialog')
  fireEvent.click(within(dialog).getByRole('group'))
  expect(onNode).not.toHaveBeenCalled()
  expect(within(dialog).queryByRole('button', { name: 'Source file' })).toBeNull()
})
