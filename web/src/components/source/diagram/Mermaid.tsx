import { Maximize2Icon, RotateCcwIcon, ZoomInIcon, ZoomOutIcon } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { highlight, splitHidden } from '../../../lib/style/highlight'
import { Button } from '../../ui/controls/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '../../ui/overlay/dialog'
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
  const [open, setOpen] = useState(false)
  const [view, setView] = useState({ s: 1, x: 0, y: 0 })
  const viewport = useRef<HTMLDivElement>(null)
  const drag = useRef<{ id: number; x: number; y: number } | null>(null)
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
          () => {
            if (live) {
              setFailed(true)
              setOpen(false)
            }
          },
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
  }, [svg, pressable, open])
  // A wheel listener with preventDefault must not be passive; React's onWheel is.
  useEffect(() => {
    const el = viewport.current
    if (!open || !el) return
    const wheel = (e: WheelEvent) => {
      e.preventDefault()
      const r = el.getBoundingClientRect()
      const px = e.clientX - r.left
      const py = e.clientY - r.top
      setView((v) => {
        const s = Math.min(6, Math.max(0.3, v.s * (e.deltaY < 0 ? 1.1 : 0.9)))
        const k = s / v.s
        return { s, x: px - (px - v.x) * k, y: py - (py - v.y) * k }
      })
    }
    el.addEventListener('wheel', wheel, { passive: false })
    return () => el.removeEventListener('wheel', wheel)
  }, [open])
  const zoom = (k: number) => setView((v) => ({ ...v, s: Math.min(6, Math.max(0.3, v.s * k)) }))
  const reset = () => setView({ s: 1, x: 0, y: 0 })
  // The inline SVG goes while the dialog shows: two copies would share Mermaid's element ids.
  const expand = (
    <div className="mb-2 flex justify-end print:hidden">
      <Button
        variant="ghost"
        size="sm"
        onClick={() => {
          reset()
          setOpen(true)
        }}
      >
        <Maximize2Icon /> 全螢幕
      </Button>
    </div>
  )
  const full = (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        setOpen(v)
        if (v) reset()
      }}
    >
      <DialogContent
        aria-describedby={undefined}
        className="h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-none print:hidden sm:max-w-none [&>div]:h-full [&>div]:grid-rows-[auto_minmax(0,1fr)] [&>div]:gap-0 [&>div]:overflow-hidden [&>div]:p-0"
      >
        <DialogHeader className="flex-row items-center justify-between gap-2 border-b px-4 py-3 pr-12 text-left">
          <DialogTitle className="min-w-0 truncate font-sans text-base">{label}</DialogTitle>
          <div className="flex items-center gap-1">
            <Button variant="ghost" size="sm" aria-label="縮小" onClick={() => zoom(0.8)}>
              <ZoomOutIcon />
            </Button>
            <Button variant="ghost" size="sm" aria-label="重設" onClick={reset}>
              <RotateCcwIcon />
            </Button>
            <Button variant="ghost" size="sm" aria-label="放大" onClick={() => zoom(1.25)}>
              <ZoomInIcon />
            </Button>
          </div>
        </DialogHeader>
        <div
          ref={viewport}
          role="img"
          aria-label={label}
          className="relative min-h-0 flex-1 cursor-grab touch-none overflow-hidden select-none active:cursor-grabbing"
          onPointerDown={(e) => {
            if (e.button !== 0) return
            e.preventDefault()
            e.currentTarget.setPointerCapture(e.pointerId)
            drag.current = { id: e.pointerId, x: e.clientX, y: e.clientY }
          }}
          onPointerMove={(e) => {
            const d = drag.current
            if (!d || d.id !== e.pointerId) return
            const dx = e.clientX - d.x
            const dy = e.clientY - d.y
            drag.current = { ...d, x: e.clientX, y: e.clientY }
            setView((v) => ({ ...v, x: v.x + dx, y: v.y + dy }))
          }}
          onPointerUp={() => {
            drag.current = null
          }}
          onPointerCancel={() => {
            drag.current = null
          }}
          onLostPointerCapture={() => {
            drag.current = null
          }}
        >
          <div
            className="w-full origin-top-left [&_svg]:!h-auto [&_svg]:!max-w-none"
            style={{ transform: `translate(${view.x}px, ${view.y}px) scale(${view.s})` }}
            // biome-ignore lint/security/noDangerouslySetInnerHtml: draw() renders at Mermaid's strict security level, which sanitises the SVG
            dangerouslySetInnerHTML={{ __html: svg }}
          />
        </div>
      </DialogContent>
    </Dialog>
  )
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
      <>
        {expand}
        {!open && (
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
        )}
        {full}
      </>
    )
  }
  return (
    <>
      {expand}
      {!open && (
        // biome-ignore lint/security/noDangerouslySetInnerHtml: draw() renders at Mermaid's strict security level, which sanitises the SVG
        <div role="img" aria-label={label} className="diagram" dangerouslySetInnerHTML={{ __html: svg }} />
      )}
      {full}
    </>
  )
}
