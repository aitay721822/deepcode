import { useEffect, useState } from 'react'
import { highlight, splitHidden } from '../../../lib/style/highlight'
import '../source.css'

type MermaidApi = typeof import('mermaid').default

// Mermaid is large: it is fetched the first time a diagram shows, as a chunk of its own; a failed fetch is tried
// again by the next diagram.
let loading: Promise<MermaidApi> | undefined
const load = () => (loading ??= import('mermaid').then((m) => m.default, (e: unknown) => {
  loading = undefined
  throw e
}))

// One diagram renders at a time: initialize() sets the configuration the next render reads. Each render has an id
// of its own: render() first removes the element holding its id, which would be the diagram on show.
let queue: Promise<unknown> = Promise.resolve()
let renders = 0

const token = (name: string) => getComputedStyle(document.documentElement).getPropertyValue(name).trim()

// The diagram's colours are the page's semantic tokens, read as it renders, so it follows the theme.
function themeVariables() {
  const [text, line, node, edge, card, muted] = ['--foreground', '--brand', '--chip', '--input', '--card', '--muted'].map(token)
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

/**
 * A Mermaid diagram: one a wiki page or answer holds, or the one drawn from the import graph (label names it). Text
 * Mermaid cannot read, or that hides characters, shows as the code it is, those characters marked.
 */
export function Mermaid({ code, label = 'Diagram' }: { code: string; label?: string }) {
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
    const wait = setTimeout(() => draw(code).then((s) => {
      if (live) {
        setSvg(s)
        setFailed(false)
      }
    }, () => live && setFailed(true)), 250)
    return () => {
      live = false
      clearTimeout(wait)
    }
  }, [code, theme])
  if (hides || failed) {
    return <pre><code className="hljs" dangerouslySetInnerHTML={{ __html: highlight(code, '') }} /></pre>
  }
  return svg
    ? <div role="img" aria-label={label} className="diagram" dangerouslySetInnerHTML={{ __html: svg }} />
    : <div role="status" aria-label="Drawing the diagram" className="diagram min-h-40 animate-pulse" />
}
