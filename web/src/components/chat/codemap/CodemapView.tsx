import { ChevronDownIcon } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { type ReactNode, useId, useState } from 'react'
import { cn } from '../../../lib/style/cn'
import { rise } from '../../../lib/style/motion'
import type { Codemap, CodemapLocation, CodemapTrace } from '../../../types/repo/chat'
import type { Citation } from '../../../types/repo/code'
import type { Repo } from '../../../types/repo/repo'
import { Markdown } from '../../source/Markdown'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '../../ui/display/tabs'
import { CodemapDiagram } from './CodemapDiagram'

/** Prose with `code` in backticks, as the model writes names: the names in code type. */
function inline(text: string): ReactNode[] {
  return text.split('`').map((part, i) =>
    i % 2 ? (
      // biome-ignore lint/suspicious/noArrayIndexKey: the parts of one string, in order
      <code key={i} className="rounded-sm bg-code px-1 font-mono text-[0.9em]">
        {part}
      </code>
    ) : (
      part
    ),
  )
}

const citation = (l: CodemapLocation): Citation => ({ path: l.path, startLine: l.line, endLine: l.line })

/**
 * An answer drawn as a code map, laid out as DeepWiki lays one out: numbered traces, each a run of lettered code
 * locations in the order the code runs, with a guide that opens under it; or the same map as a diagram. A location
 * opens its line beside the answer (onCite), and the one on show there (selected) keeps a grey ground. top is the map
 * title's heading level, the traces' one below it. The diagram is drawn when first asked for, and kept while the list
 * shows.
 */
export function CodemapView({
  map,
  repo,
  top,
  selected,
  onCite,
}: {
  map: Codemap
  repo: Repo
  top: 'h2' | 'h3'
  selected: Citation | null
  onCite: (c: Citation) => void
}) {
  const [view, setView] = useState('list')
  const [drawn, setDrawn] = useState(false)
  const open = (l: CodemapLocation) => onCite(citation(l))
  const shown = (l: CodemapLocation) =>
    selected?.path === l.path && selected.startLine === l.line && selected.endLine === l.line
  const Title = top
  return (
    <Tabs
      value={view}
      onValueChange={(v) => {
        setView(v)
        if (v === 'diagram') setDrawn(true)
      }}
      // Model prose names long paths in backticks: it breaks anywhere rather than widen a phone's page.
      className="gap-4 [overflow-wrap:anywhere]"
    >
      <div className="flex flex-wrap items-start justify-between gap-x-6 gap-y-2">
        <div className="min-w-0 flex-1 basis-64">
          <Title className="font-serif text-[22px] leading-tight font-normal tracking-[-0.015em] [overflow-wrap:anywhere]">
            {map.title}
          </Title>
          {map.description && <p className="mt-1.5 text-[14.5px] text-muted-foreground">{inline(map.description)}</p>}
        </div>
        <TabsList aria-label="Codemap 檢視方式">
          <TabsTrigger value="list">列表</TabsTrigger>
          <TabsTrigger value="diagram">圖表</TabsTrigger>
        </TabsList>
      </div>
      {/* Both views stay mounted once shown, so the opened guides and the drawn diagram outlast a switch. */}
      <TabsContent value="list" forceMount className="grid gap-7 data-[state=inactive]:hidden">
        {map.traces.map((t) => (
          <TraceBlock
            key={t.id}
            trace={t}
            repo={repo}
            heading={top === 'h2' ? 'h3' : 'h4'}
            shown={shown}
            onOpen={open}
          />
        ))}
      </TabsContent>
      <TabsContent
        value="diagram"
        forceMount={drawn || undefined}
        className="data-[state=inactive]:hidden [&_.diagram]:mb-0" // the card ends it, not a wiki page's next paragraph
      >
        <CodemapDiagram map={map} onOpen={open} />
      </TabsContent>
    </Tabs>
  )
}

function TraceBlock({
  trace,
  repo,
  heading: Heading,
  shown,
  onOpen,
}: {
  trace: CodemapTrace
  repo: Repo
  heading: 'h3' | 'h4'
  shown: (l: CodemapLocation) => boolean
  onOpen: (l: CodemapLocation) => void
}) {
  const [guide, setGuide] = useState(false)
  const guideId = useId()
  return (
    // No named section: a map's traces are no landmarks to jump between; their headings lead to them.
    <div>
      <div className="flex items-start gap-3">
        <span
          aria-hidden="true"
          className="flex size-7 shrink-0 items-center justify-center rounded-md bg-brand/12 font-mono text-[13px] font-medium text-brand"
        >
          {trace.id}
        </span>
        <div className="min-w-0 flex-1">
          <Heading className="text-[16px] leading-snug font-semibold">
            <span className="sr-only">{trace.id}. </span>
            {trace.title}
          </Heading>
          {trace.description && <p className="mt-1 text-sm text-muted-foreground">{inline(trace.description)}</p>}
        </div>
      </div>
      {/* The thread down the left joins the trace's locations in the order the code runs. */}
      <ol className="mt-3 ml-3.5 grid gap-2 border-l pl-4">
        {trace.locations.map((l) => (
          <li key={l.id} className="min-w-0">
            <button
              type="button"
              aria-current={shown(l) || undefined}
              className={cn(
                'grid w-full gap-1 rounded-lg border bg-card px-3 py-2.5 text-left transition-colors hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-hidden',
                shown(l) && 'bg-muted hover:bg-muted',
              )}
              onClick={() => onOpen(l)}
            >
              <span className="flex min-w-0 flex-wrap items-baseline gap-x-2">
                <span className="font-mono text-[12px] font-medium text-brand">{l.id}</span>
                <span className="text-[14px] font-medium">{l.title}</span>
                <span
                  className="ml-auto min-w-0 font-mono text-[11.5px] text-muted-foreground [overflow-wrap:anywhere]"
                  dir="ltr"
                >
                  {l.path}:{l.line}
                </span>
              </span>
              {l.description && <span className="text-[13.5px] text-muted-foreground">{inline(l.description)}</span>}
              <code className="block truncate rounded-md bg-code px-2 py-1 font-mono text-[12px]" dir="ltr">
                {l.code}
              </code>
            </button>
          </li>
        ))}
      </ol>
      {trace.guide && (
        <div className="mt-2 ml-7.5">
          <button
            type="button"
            aria-expanded={guide}
            aria-controls={guideId}
            className="inline-flex items-center gap-1 rounded-sm text-[13px] text-brand underline underline-offset-4 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-hidden"
            onClick={() => setGuide(!guide)}
          >
            <ChevronDownIcon className={cn('size-3.5 transition-transform', guide && 'rotate-180')} />
            {guide ? '收合導覽' : '看更多：這條路徑的導覽'}
          </button>
          <div id={guideId}>
            <AnimatePresence initial={false}>
              {guide && (
                <motion.div className="mt-2 rounded-lg bg-muted/60 px-4 py-3 text-[14px]" {...rise}>
                  <Markdown
                    text={trace.guide}
                    citations={[]}
                    repo={repo}
                    onCite={() => {}}
                    top={Heading === 'h3' ? 'h4' : 'h5'}
                  />
                </motion.div>
              )}
            </AnimatePresence>
          </div>
        </div>
      )}
    </div>
  )
}
