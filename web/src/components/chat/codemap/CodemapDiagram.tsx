import { useMemo } from 'react'
import { splitHidden } from '../../../lib/style/highlight'
import type { Codemap, CodemapLocation } from '../../../types/repo/chat'
import { Mermaid } from '../../source/diagram/Mermaid'

// A label holds text only: what Mermaid would read as syntax (quotes, tags, the markdown backtick, brackets, its own
// #entity; codes, the colon its style-line clean-up keys on, an HTML entity's &) becomes an entity code, so
// app/[slug]/page.tsx and List<T> show as written; line breaks (a %% line is a comment) and the hidden characters for
// which Mermaid.tsx shows a diagram as code go.
const label = (s: string) =>
  splitHidden(s)
    .filter((_, i) => i % 2 === 0)
    .join(' ')
    .replace(/[\s\p{Cf}]+/gu, ' ')
    .trim()
    .replace(/[#"`<>{}[\]|:&]/g, (c) => `#${c.charCodeAt(0)};`)

/** The map as a Mermaid flowchart: a box per trace holding its locations, each pointing to the next. */
export function flowchart(map: Codemap) {
  const lines = ['flowchart TB']
  for (const t of map.traces) {
    lines.push(`  subgraph T${t.id}["${t.id}. ${label(t.title)}"]`, '    direction TB')
    t.locations.forEach((l, i) => {
      lines.push(`    L${l.id}["${l.id} ${label(l.title)}<br/>${label(l.path)}:${l.line}"]`)
      if (i > 0) lines.push(`    L${t.locations[i - 1].id} --> L${l.id}`)
    })
    lines.push('  end')
  }
  return lines.join('\n')
}

/** The code map drawn as a diagram; a location's node opens its line, as in the list. */
export function CodemapDiagram({ map, onOpen }: { map: Codemap; onOpen: (l: CodemapLocation) => void }) {
  const code = useMemo(() => flowchart(map), [map])
  const nodes = useMemo(() => new Map(map.traces.flatMap((t) => t.locations.map((l) => [`L${l.id}`, l]))), [map])
  return (
    <Mermaid
      code={code}
      label={`${map.title}：Codemap 圖`}
      onNode={(id) => {
        const l = nodes.get(id)
        if (l) onOpen(l)
      }}
    />
  )
}
