import { expect, it } from 'vitest'
import type { CodemapTrace } from '../../../types/repo/chat'
import { flowchart } from './CodemapDiagram'

const trace = (id: string): CodemapTrace => ({
  id,
  title: `Trace ${id}`,
  description: '',
  guide: '',
  locations: [{ id: `${id}a`, title: 'entry', description: '', path: 'main.go', line: 1, code: '' }],
})

it('chains the traces left to right in their order', () => {
  const code = flowchart({ title: 'Map', description: '', traces: ['1', '2', '3'].map(trace) })
  expect(code.split('\n')[0]).toBe('flowchart LR')
  expect(code.split('\n').filter((l) => l.includes('~~~'))).toEqual(['  T1 ~~~ T2', '  T2 ~~~ T3'])
})

it('links nothing for a single trace', () => {
  expect(flowchart({ title: 'Map', description: '', traces: [trace('1')] })).not.toContain('~~~')
})
