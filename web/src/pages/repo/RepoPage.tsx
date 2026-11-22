import { Link, useNavigate } from '@tanstack/react-router'
import { ChevronRightIcon } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { type Ref, useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { getWiki } from '../../api/repo/wiki'
import { AskBox } from '../../components/chat/AskBox'
import { Notice } from '../../components/layout/Notice'
import { JobProgress } from '../../components/repo/JobProgress'
import { isActive } from '../../components/repo/jobStage'
import { RepoNotice } from '../../components/repo/RepoNotice'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '../../components/ui/display/tabs'
import { LoadError } from '../../components/ui/feedback/load-error'
import { Dialog, DialogTitle, SheetContent } from '../../components/ui/overlay/dialog'
import { formatDate, shortSha } from '../../lib/format'
import { cn } from '../../lib/style/cn'
import { rise } from '../../lib/style/motion'
import { useApi } from '../../lib/useApi'
import type { Citation } from '../../types/repo/code'
import { Frame } from '../Frame'
import { useRepo } from '../useRepo'
import { BranchCard } from './BranchCard'
import { CitedFile } from './files/CitedFile'
import { FilesPanel } from './files/FilesPanel'
import { FileView } from './files/FileView'
import { SyncCard } from './SyncCard'
import { useChatModel } from './search/useChatModel'
import { useSync } from './useSync'
import { WebhookCard } from './WebhookCard'
import { Toc, type TocItem } from './wiki/Toc'
import { WikiArticle } from './wiki/WikiArticle'
import { WikiNav } from './wiki/WikiNav'
import { WikiPlaceholder } from './wiki/WikiPlaceholder'

type Tab = 'wiki' | 'files'
const LEFT_RAIL_MEDIA = '(min-width: 900px)'
const RIGHT_RAIL_MEDIA = '(min-width: 1240px)'

/** ?page= a wiki page; or ?file= a file, L= its lines (5-9), s= the snapshot it is read at (links made before wiki
 * citations opened in the drawer: nothing writes it now). */
type RepoSearch = { page?: string; file?: string; L?: string; s?: string }

function MobileControls({ initiallyOpen, hostRef }: { initiallyOpen: boolean; hostRef: Ref<HTMLDivElement> }) {
  const [open, setOpen] = useState(initiallyOpen)
  return (
    <details
      className="group border-b print:hidden min-[900px]:hidden"
      open={open}
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary className="flex cursor-pointer list-none items-center gap-2.5 px-4 py-3 font-mono text-[11.5px] font-medium tracking-[0.12em] [&::-webkit-details-marker]:hidden">
        <ChevronRightIcon
          aria-hidden="true"
          className="size-3.5 text-brand transition-transform group-open:rotate-90"
        />
        管理 Repository
      </summary>
      <div className="px-4 pb-4" ref={hostRef} />
    </details>
  )
}

export function RepoPage({ id, search }: { id: string; search: RepoSearch }) {
  const navigate = useNavigate()
  const { data: repo, error, status, reload } = useRepo(id)
  const { health, error: healthError, ready: canAsk, unavailable } = useChatModel()
  const canSync = repo?.permissions.includes('repo.sync') ?? false
  const mobileControls = useRef<HTMLDivElement>(null)
  const leftControls = useRef<HTMLDivElement>(null)
  const rightControls = useRef<HTMLDivElement>(null)
  const [controlsHost] = useState(() => document.createElement('div'))
  useLayoutEffect(() => {
    if (!canSync) return
    const leftRail = window.matchMedia(LEFT_RAIL_MEDIA)
    const wide = window.matchMedia(RIGHT_RAIL_MEDIA)
    // Move the portal host, not the cards: resizing must retain the one-time webhook secret and not refetch.
    const placeControls = () => {
      const target = wide.matches ? rightControls : leftRail.matches ? leftControls : mobileControls
      if (target.current && controlsHost.parentElement !== target.current) target.current.append(controlsHost)
    }
    placeControls()
    leftRail.addEventListener('change', placeControls)
    wide.addEventListener('change', placeControls)
    return () => {
      leftRail.removeEventListener('change', placeControls)
      wide.removeEventListener('change', placeControls)
      controlsHost.remove()
    }
  }, [canSync, controlsHost])
  const sync = useSync(id, reload) // Retry of a failed job, Sync when there is no wiki
  const ready = repo?.snapshot
  const wikiBusy = repo?.job?.kind === 'wiki' && isActive(repo.job)
  const {
    data: wiki,
    error: wikiError,
    reload: reloadWiki,
  } = useApi(
    repo && ready ? `${repo.id}:${ready.id}` : null,
    () => getWiki(id), // the repository useRepo(id) loaded
    wikiBusy ? 3000 : undefined,
  )
  // Pick up the last page once generation stops; on the first render the wiki is being fetched already.
  const wasBusy = useRef(wikiBusy)
  useEffect(() => {
    if (wasBusy.current && !wikiBusy) reloadWiki()
    wasBusy.current = wikiBusy
  }, [wikiBusy, reloadWiki])

  const file = search.file ?? null
  const lines = search.L?.split('-')
    .map(Number)
    .filter(Boolean)
    .sort((a, b) => a - b) // "5-" → [5], "9-3" → [3, 9]
  const fileSnapshot = search.s ?? null
  const [tab, setTab] = useState<Tab>(file ? 'files' : 'wiki')
  useEffect(() => setTab(file ? 'files' : 'wiki'), [file]) // follow back/forward too
  const [toc, setToc] = useState<TocItem[]>([])
  // What the drawer shows (openCitation); kept once it closes, while the drawer slides out. opener, the chip or file
  // that opened it, takes focus back on close: with no Dialog.Trigger, Radix would leave it on the body.
  const [cited, setCited] = useState<{
    snapshot: string
    sha: string
    citation: Pick<Citation, 'path'> & Partial<Citation>
    opener: HTMLElement | null
    open: boolean
  } | null>(null)
  const closeCitation = useCallback(() => setCited((c) => c && { ...c, open: false }), []) // memo(CitedFile) holds

  if (!repo) {
    return (
      <Frame>
        <main>
          <RepoNotice error={error} status={status} onRetry={reload} />
        </main>
      </Frame>
    )
  }

  const here = { to: '/repos/$id', params: { id: repo.id } } as const
  const page = search.page === undefined ? wiki?.pages[0] : wiki?.pages.find((p) => p.slug === search.page)
  const pageNotFound = search.page !== undefined && !!wiki && !page
  // The wiki job's card: once it failed (above any pages written before), or while it runs before the first page exists.
  const job = repo.job
  const wikiJob = job?.kind === 'wiki' && (job.status === 'failed' || (wikiBusy && !page)) ? job : null
  // The file tree and symbol search list the published snapshot, so their files open there.
  const openFile = (path: string, start?: number, end?: number) =>
    navigate({ ...here, search: { file: path, L: start ? `${start}-${end ?? start}` : undefined } })
  // A wiki citation, or a file of the page, opens in the drawer over the page, which keeps its place, at the wiki's
  // snapshot, which trails the published one while the next wiki is written.
  const openCitation = (path: string, startLine?: number, endLine?: number) =>
    wiki &&
    setCited({
      snapshot: wiki.snapshotId,
      sha: wiki.commitSha,
      citation: { path, startLine, endLine },
      opener: document.activeElement as HTMLElement | null,
      open: true,
    })
  // The commit a file is shown at, known before (or without) the file loading: s= comes from links to a wiki
  // citation made before citations opened in the drawer.
  const fileSha = fileSnapshot
    ? wiki && wiki.snapshotId === fileSnapshot
      ? wiki.commitSha
      : undefined
    : ready?.commitSha
  // Whether the main column shows the latest job in a card: the first index job, or the wiki job above the wiki.
  const jobCard = ready ? !file && !!wikiJob : !!job
  return (
    <Frame repo={repo}>
      <div className="grid grid-cols-1 min-[900px]:grid-cols-[260px_minmax(0,1fr)] min-[1240px]:grid-cols-[300px_minmax(0,1fr)_300px]">
        <aside
          aria-label="Wiki 頁面與檔案"
          className={cn(
            'max-h-[45dvh] print:hidden overflow-y-auto overscroll-contain border-b px-4 pt-7 pb-4 min-[900px]:sticky min-[900px]:top-16 min-[900px]:h-[calc(100dvh-4rem)] min-[900px]:max-h-none min-[900px]:border-r min-[900px]:border-b-0 min-[900px]:pr-[18px] min-[900px]:pb-36 min-[900px]:pl-6',
            !ready && 'max-[900px]:hidden',
          )}
        >
          {ready && (
            <p className="mb-5 font-mono text-[10.5px] font-medium tracking-[0.08em] text-muted-foreground uppercase">
              索引於 {formatDate(ready.indexedAt)} ·{' '}
              <span className="text-foreground normal-case">{shortSha(ready.commitSha)}</span>
            </p>
          )}
          {canSync && <div className="mb-4 hidden min-[900px]:block min-[1240px]:hidden" ref={leftControls} />}
          {ready && (
            <Tabs value={tab} onValueChange={(t) => (t === 'wiki' && file ? navigate(here) : setTab(t as Tab))}>
              <TabsList aria-label="瀏覽 Repository">
                <TabsTrigger value="wiki">Wiki</TabsTrigger>
                <TabsTrigger value="files">檔案</TabsTrigger>
              </TabsList>
              <TabsContent value="wiki">
                <WikiNav
                  pages={wiki?.pages ?? []}
                  active={file ? undefined : page?.slug}
                  repoId={repo.id}
                  writing={wikiBusy}
                />
              </TabsContent>
              <TabsContent value="files">
                <FilesPanel repo={repo} snapshot={ready.id} active={file} onOpen={openFile} />
              </TabsContent>
            </Tabs>
          )}
        </aside>

        {canSync && <MobileControls initiallyOpen={!ready} hostRef={mobileControls} />}

        <main
          tabIndex={-1}
          className="mx-auto w-full max-w-[860px] px-4 pt-6 pb-44 outline-none min-[900px]:px-12 min-[900px]:pt-9"
        >
          {/* A poll that failed keeps the repository as it was: say so, or a job's card would seem to stand still. */}
          <LoadError className="mb-6" error={error} onRetry={reload} />
          <AnimatePresence mode="wait" initial={false}>
            <motion.div
              key={!ready ? 'indexing' : file ? `file:${fileSnapshot}:${file}` : page ? `page:${page.slug}` : 'wiki'}
              className="space-y-8"
              {...rise}
            >
              {!ready ? (
                repo.job ? (
                  <JobProgress
                    job={repo.job}
                    title={`為 ${repo.owner}/${repo.name} 建立索引`}
                    retry={canSync ? sync : undefined}
                  />
                ) : (
                  <p className="text-muted-foreground">尚未建立索引。</p>
                ) // its first job was never queued, or is gone: Sync queues it
              ) : file ? (
                <FileView repo={repo} path={file} lines={lines} snapshot={fileSnapshot} sha={fileSha} />
              ) : (
                <>
                  {wikiJob && <JobProgress job={wikiJob} title="產生 Wiki" retry={canSync ? sync : undefined} />}
                  {page ? (
                    <WikiArticle page={page} repo={repo} writing={wikiBusy} onToc={setToc} onCite={openCitation} />
                  ) : pageNotFound ? (
                    <Notice title="找不到 Wiki 頁面">
                      <p className="text-muted-foreground [overflow-wrap:anywhere]">找不到頁面「{search.page}」。</p>
                      <Link {...here} search={{}} className="text-brand underline underline-offset-4">
                        返回 Overview
                      </Link>
                    </Notice>
                  ) : (
                    !wikiJob && (
                      <WikiPlaceholder
                        repo={repo}
                        health={healthError ? null : health}
                        wiki={wiki}
                        error={wikiError}
                        onReload={reloadWiki}
                        sync={canSync ? sync : undefined}
                      />
                    )
                  )}
                </>
              )}
            </motion.div>
          </AnimatePresence>
        </main>

        <aside
          aria-label="同步與本頁內容"
          className="sticky top-16 hidden print:hidden h-[calc(100dvh-4rem)] overflow-y-auto px-6 pt-7 pb-36 min-[1240px]:block"
        >
          {canSync && <div className="mb-7" ref={rightControls} />}
          {!file && page?.content && toc.length > 0 && <Toc items={toc} />}
        </aside>
      </div>
      {canSync &&
        createPortal(
          <div className="flex flex-col gap-3">
            <SyncCard repo={repo} jobCard={jobCard} onSynced={reload} />
            <BranchCard repo={repo} onChanged={reload} />
            <WebhookCard repo={repo} />
          </div>,
          controlsHost,
        )}
      {cited && (
        <Dialog open={cited.open} onOpenChange={closeCitation}>
          <SheetContent
            aria-describedby={undefined}
            onCloseAutoFocus={(e) => {
              e.preventDefault()
              cited.opener?.focus()
            }}
          >
            <DialogTitle className="sr-only">{cited.citation.path}</DialogTitle>
            <CitedFile
              repo={repo}
              snapshot={cited.snapshot}
              sha={cited.sha}
              citation={cited.citation}
              onClose={closeCitation}
              className="rounded-xl shadow-modal"
            />
          </SheetContent>
        </Dialog>
      )}
      {repo.permissions.includes('repo.chat') && (
        <AskBox
          placeholder={unavailable || `針對 ${repo.owner}/${repo.name} 提問`}
          disabled={!ready || !canAsk}
          // Over the main column, clear of the side columns: 260px from 900px wide, 300px each from 1240px.
          className="min-[900px]:pl-[276px] min-[1240px]:px-[316px]"
          onAsk={(q, mode) =>
            navigate({
              to: '/repos/$id/search/{-$conversationId}',
              params: { id: repo.id, conversationId: undefined },
              state: { question: q, mode }, // the search page asks it
            })
          }
        />
      )}
    </Frame>
  )
}
