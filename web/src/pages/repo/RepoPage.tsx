import { useNavigate } from '@tanstack/react-router'
import { AnimatePresence, motion } from 'motion/react'
import { useEffect, useRef, useState } from 'react'
import { getHealth } from '../../api/repo/repos'
import { getWiki } from '../../api/repo/wiki'
import { AskBox } from '../../components/chat/AskBox'
import { JobProgress } from '../../components/repo/JobProgress'
import { isActive } from '../../components/repo/jobStage'
import { RepoNotice } from '../../components/repo/RepoNotice'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '../../components/ui/display/tabs'
import { LoadError } from '../../components/ui/feedback/load-error'
import { formatDate, shortSha } from '../../lib/format'
import { rise } from '../../lib/style/motion'
import { useApi } from '../../lib/useApi'
import { Frame } from '../Frame'
import { useRepo } from '../useRepo'
import { FilesPanel } from './files/FilesPanel'
import { FileView } from './files/FileView'
import { SyncCard } from './SyncCard'
import { useSync } from './useSync'
import { Toc, type TocItem } from './wiki/Toc'
import { WikiArticle } from './wiki/WikiArticle'
import { WikiNav } from './wiki/WikiNav'
import { WikiPlaceholder } from './wiki/WikiPlaceholder'

type Tab = 'wiki' | 'files'

/** ?page= a wiki page; or ?file= a file, L= its lines (5-9), s= the snapshot it is read at. */
type RepoSearch = { page?: string; file?: string; L?: string; s?: string }

export function RepoPage({ id, search }: { id: string; search: RepoSearch }) {
  const navigate = useNavigate()
  const { data: repo, error, status, reload } = useRepo(id)
  const { data: health, error: healthError } = useApi('health', getHealth)
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
  const page = wiki?.pages.find((p) => p.slug === search.page) ?? wiki?.pages[0]
  const canSync = repo.permissions.includes('repo.sync')
  // The wiki job's card: once it failed (above any pages written before), or while it runs before the first page exists.
  const job = repo.job
  const wikiJob = job?.kind === 'wiki' && (job.status === 'failed' || (wikiBusy && !page)) ? job : null
  // The file tree and symbol search list the published snapshot, so their files open there.
  const openFile = (path: string, start?: number, end?: number, snapshot?: number) =>
    navigate({
      ...here,
      search: {
        file: path,
        L: start ? `${start}-${end ?? start}` : undefined,
        s: snapshot ? String(snapshot) : undefined,
      },
    })
  // A wiki citation points into the wiki's snapshot, which trails the published one while the next wiki is written.
  const openCitation = (path: string, start?: number, end?: number) =>
    openFile(path, start, end, wiki && ready && wiki.snapshotId !== ready.id ? wiki.snapshotId : undefined)
  // The commit a file is shown at, known before (or without) the file loading: s= comes from the wiki's citations.
  const fileSha = fileSnapshot
    ? wiki && String(wiki.snapshotId) === fileSnapshot
      ? wiki.commitSha
      : undefined
    : ready?.commitSha
  // Whether the main column shows the latest job in a card: the first index job, or the wiki job above the wiki.
  const jobCard = ready ? !file && !!wikiJob : !!job
  const syncCard = canSync && <SyncCard repo={repo} jobCard={jobCard} onSynced={reload} />

  return (
    <Frame repo={repo}>
      <div className="grid grid-cols-1 min-[900px]:grid-cols-[260px_minmax(0,1fr)] min-[1240px]:grid-cols-[300px_minmax(0,1fr)_300px]">
        <aside
          aria-label="Wiki 頁面與檔案"
          className="max-h-[45dvh] print:hidden overflow-y-auto overscroll-contain border-b px-4 pt-7 pb-4 min-[900px]:sticky min-[900px]:top-16 min-[900px]:h-[calc(100dvh-4rem)] min-[900px]:max-h-none min-[900px]:border-r min-[900px]:border-b-0 min-[900px]:pr-[18px] min-[900px]:pb-36 min-[900px]:pl-6"
        >
          {ready && (
            <p className="mb-5 font-mono text-[10.5px] font-medium tracking-[0.08em] text-muted-foreground uppercase">
              索引於 {formatDate(ready.indexedAt)} ·{' '}
              <span className="text-foreground normal-case">{shortSha(ready.commitSha)}</span>
            </p>
          )}
          {/* Sync lives in the rail; once the rail is hidden, here. */}
          {syncCard && <div className="mb-4 min-[1240px]:hidden">{syncCard}</div>}
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
          {syncCard && <div className="mb-7">{syncCard}</div>}
          {!file && page?.content && toc.length > 0 && <Toc items={toc} />}
        </aside>
      </div>
      {repo.permissions.includes('repo.chat') && (
        <AskBox
          placeholder={
            healthError
              ? `目前無法提問：${healthError}`
              : health && !health.chatModel
                ? '請先設定 DEEPCODE_CHAT_MODEL 才能提問'
                : `針對 ${repo.owner}/${repo.name} 提問`
          }
          disabled={!ready || !health?.chatModel}
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
