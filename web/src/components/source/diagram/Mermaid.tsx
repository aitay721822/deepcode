import { useEffect, useRef, useState } from 'react'
import { highlight, splitHidden } from '../../../lib/style/highlight'
import '../source.css'

type MermaidApi = typeof import('mermaid').default

// Mermaid is large: it is fetched the first time a diagram shows, as a chunk of its own; a failed fetch is tried
// again by the next diagram.
let loading: Promise<MermaidApi> | undefined
const load = () =>
  (loading ??= import('mermaid').then(
    (m) => m.default,
    (e: unknown) => {
      loading = undefined
      throw e
    },
  ))

// One diagram renders at a time: initialize() sets the configuration the next render reads. Each render has an id
// of its own: render() first removes the element holding its id, which would be the diagram on show.
let queue: Promise<unknown> = Promise.resolve()
let renders = 0

const token = (name: string) => getComputedStyle(document.documentElement).getPropertyValue(name).trim()

// The diagram's colours are the page's semantic tokens, read as it renders, so it follows the theme.
function themeVariables() {
  const [text, line, node, edge, card, muted] = [
    '--foreground',
    '--brand',
    '--chip',
    '--input',
    '--card',
    '--muted',
  ].map(token)
  return {
    fontFamily: "'IBM Plex Mono', ui-monospace, monospace",
    fontSize: '13px',
    background: card,
    primaryColor: node,
    primaryTextColor: text,
    primaryBorderColor: edge,
    secondaryColor: muted,
    tertiaryColor: card,
    textColor: text,
    lineColor: line,
    mainBkg: node,
    nodeBorder: edge,
    clusterBkg: muted,
    clusterBorder: edge,
    edgeLabelBackground: card,
    actorBkg: node,
    actorBorder: edge,
    actorTextColor: text,
    signalColor: text,
    signalTextColor: text,
    noteBkgColor: muted,
    noteTextColor: text,
    noteBorderColor: edge,
  }
}

async function draw(code: string) {
  const mermaid = await load()
  const id = `mermaid-${++renders}`
  const run = queue.then(async () => {
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: 'strict', // labels are text: no HTML, no click handlers
      suppressErrorRendering: true, // a syntax error throws; the caller shows the code instead
      theme: 'base',
      themeVariables: themeVariables(),
    })
    return (await mermaid.render(id, code)).svg
  })
  queue = run.catch(() => undefined)
  return run
}

/** The id a flowchart node was given in code, read from the id Mermaid gives its element (mermaid-3-flowchart-L1a-0). */
const nodeId = (target: EventTarget) => (target as Element).closest?.('g.node')?.id.match(/flowchart-(.+)-\d+$/)?.[1]

/**
 * A Mermaid diagram: one a wiki page or answer holds, or the one drawn from the import graph (label names it). Text
 * Mermaid cannot read, or that hides characters, shows as the code it is, those characters marked. With onNode, a
 * flowchart's nodes are buttons (focusable, pressed with a click, Enter or Space) that hand onNode their id.
 */
export function Mermaid({
  code,
  label = '關係圖',
  onNode,
}: {
  code: string
  label?: string
  onNode?: (id: string) => void
}) {
  const box = useRef<HTMLDivElement>(null)
  const hides = splitHidden(code).length > 1
  const [svg, setSvg] = useState('')
  const [failed, setFailed] = useState(false)
  const [theme, setTheme] = useState(() => document.documentElement.className)
  // The theme switch changes <html>'s class: the diagram is drawn again in the new colours.
  useEffect(() => {
    const watch = new MutationObserver(() => setTheme(document.documentElement.className))
    watch.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
    return () => watch.disconnect()
  }, [])
  // Drawn once the text has rested a moment: an answer streams its diagram in a few characters at a time.
  useEffect(() => {
    if (hides) return
    let live = true
    const wait = setTimeout(
      () =>
        draw(code).then(
          (s) => {
            if (live) {
              setSvg(s)
              setFailed(false)
            }
          },
          () => live && setFailed(true),
        ),
      250,
    )
    return () => {
      live = false
      clearTimeout(wait)
    }
  }, [code, theme, hides])
  const pressable = !!onNode
  useEffect(() => {
    if (!pressable || !svg) return
    for (const node of box.current?.querySelectorAll('g.node') ?? []) {
      node.setAttribute('tabindex', '0')
      node.setAttribute('role', 'button')
      // Each text node apart: textContent would run a label's lines (split by <br/>) together.
      const parts: string[] = []
      for (const walk = document.createTreeWalker(node, NodeFilter.SHOW_TEXT); walk.nextNode(); ) {
        parts.push(walk.currentNode.textContent ?? '')
      }
      node.setAttribute('aria-label', parts.join(' ').replace(/\s+/g, ' ').trim())
    }
  }, [svg, pressable])
  if (hides || failed) {
    return (
      <pre>
        {/* biome-ignore lint/security/noDangerouslySetInnerHtml: highlight() escapes the code and marks its hidden characters */}
        <code className="hljs" dangerouslySetInnerHTML={{ __html: highlight(code, '') }} />
      </pre>
    )
  }
  if (!svg) return <div role="status" aria-label="正在繪製關係圖" className="diagram min-h-40 animate-pulse" />
  if (onNode) {
    const press = (target: EventTarget) => {
      const id = nodeId(target)
      if (id) onNode(id)
    }
    return (
      // biome-ignore lint/a11y/useSemanticElements: a group of buttons (the diagram's nodes), not of a form's fields
      <div
        ref={box}
        role="group"
        aria-label={label}
        className="diagram [&_g.node]:cursor-pointer [&_g.node:focus-visible]:outline-2 [&_g.node:focus-visible]:outline-ring"
        onClick={(e) => press(e.target)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault()
            press(e.target)
          }
        }}
        // biome-ignore lint/security/noDangerouslySetInnerHtml: draw() renders at Mermaid's strict security level, which sanitises the SVG
        dangerouslySetInnerHTML={{ __html: svg }}
      />
    )
  }
  // biome-ignore lint/security/noDangerouslySetInnerHtml: draw() renders at Mermaid's strict security level, which sanitises the SVG
  return <div role="img" aria-label={label} className="diagram" dangerouslySetInnerHTML={{ __html: svg }} />
}
