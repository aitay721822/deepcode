import { AnimatePresence, motion } from 'motion/react'
import { useEffect, useState } from 'react'
import { getHealth } from '../../api/repo/repos'
import { getWiki } from '../../api/repo/wiki'
import { AskBox } from '../../components/chat/AskBox'
import { JobProgress } from '../../components/repo/JobProgress'
import { isActive } from '../../components/repo/jobStage'
import { RepoNotice } from '../../components/repo/RepoNotice'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '../../components/ui/display/tabs'
import { formatDate, shortSha } from '../../lib/format'
import { navigate } from '../../lib/router'
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

export function RepoPage({ id, params }: { id: number; params: URLSearchParams }) {
  const { data: repo, error, status, reload } = useRepo(id)
  const { data: health, error: healthError } = useApi('health', getHealth)
  const sync = useSync(id, reload) // Retry of a failed job, Sync when there is no wiki
  const ready = repo?.snapshot
  const wikiBusy = repo?.job?.kind === 'wiki' && isActive(repo.job)
  const { data: wiki, error: wikiError, reload: reloadWiki } = useApi(
    repo && ready ? `${repo.id}:${ready.id}` : null,
    () => getWiki(repo!.id),
    wikiBusy ? 3000 : undefined,
  )
  useEffect(() => {
    if (!wikiBusy) reloadWiki() // pick up the last page once generation stops
  }, [wikiBusy])

  const file = params.get('file')
  const lines = params.get('L')?.split('-').map(Number).filter(Boolean).sort((a, b) => a - b) // "5-" → [5], "9-3" → [3, 9]
  const fileSnapshot = params.get('s')
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

  const base = `/repos/${repo.id}`
  const page = wiki?.pages.find((p) => p.slug === params.get('page')) ?? wiki?.pages[0]
  const canSync = repo.permissions.includes('repo.sync')
  // The wiki job's card: once it failed (above any pages written before), or while it runs before the first page exists.
  const job = repo.job
  const wikiJob = job?.kind === 'wiki' && (job.status === 'failed' || (wikiBusy && !page)) ? job : null
  // The file tree and symbol search list the published snapshot, so their files open there.
  const openFile = (path: string, start?: number, end?: number, snapshot?: number) =>
    navigate(`${base}?file=${encodeURIComponent(path)}${start ? `&L=${start}-${end ?? start}` : ''}${snapshot ? `&s=${snapshot}` : ''}`)
  // A wiki citation points into the wiki's snapshot, which trails the published one while the next wiki is written.
  const openCitation = (path: string, start?: number, end?: number) =>
    openFile(path, start, end, wiki && ready && wiki.snapshotId !== ready.id ? wiki.snapshotId : undefined)
  // The commit a file is shown at, known before (or without) the file loading: s= comes from the wiki's citations.
  const fileSha = fileSnapshot ? (wiki && String(wiki.snapshotId) === fileSnapshot ? wiki.commitSha : undefined) : ready?.commitSha
  // Whether the main column shows the latest job in a card: the first index job, or the wiki job above the wiki.
  const jobCard = ready ? !file && !!wikiJob : !!job
  const syncCard = canSync && <SyncCard repo={repo} jobCard={jobCard} onSynced={reload} />

  return (
    <Frame repo={repo}>
      <div className="grid grid-cols-1 min-[900px]:grid-cols-[260px_minmax(0,1fr)] min-[1240px]:grid-cols-[300px_minmax(0,1fr)_300px]">
        <aside className="max-h-[45dvh] overflow-y-auto border-b px-4 pt-7 pb-4 min-[900px]:sticky min-[900px]:top-16 min-[900px]:h-[calc(100dvh-4rem)] min-[900px]:max-h-none min-[900px]:border-r min-[900px]:border-b-0 min-[900px]:pr-[18px] min-[900px]:pb-36 min-[900px]:pl-6">
          {ready && <p className="mb-5 font-mono text-[10.5px] font-medium tracking-[0.08em] text-muted-foreground uppercase">Indexed {formatDate(ready.indexedAt)} · <span className="text-foreground normal-case">{shortSha(ready.commitSha)}</span></p>}
          {/* Sync lives in the rail; once the rail is hidden, here. */}
          {syncCard && <div className="mb-4 min-[1240px]:hidden">{syncCard}</div>}
          {ready && (
            <Tabs value={tab} onValueChange={(t) => (t === 'wiki' && file ? navigate(base) : setTab(t as Tab))}>
              <TabsList aria-label="Browse the repository">
                <TabsTrigger value="wiki">Wiki</TabsTrigger>
                <TabsTrigger value="files">Files</TabsTrigger>
              </TabsList>
              <TabsContent value="wiki">
                <WikiNav pages={wiki?.pages ?? []} active={file ? undefined : page?.slug} base={base} writing={wikiBusy} />
              </TabsContent>
              <TabsContent value="files">
                <FilesPanel repo={repo} active={file} onOpen={openFile} />
              </TabsContent>
            </Tabs>
          )}
        </aside>

        <main className="mx-auto w-full max-w-[860px] px-4 pt-6 pb-44 min-[900px]:px-12 min-[900px]:pt-9">
          <AnimatePresence mode="wait" initial={false}>
            <motion.div key={!ready ? 'indexing' : file ? `file:${fileSnapshot}:${file}` : page ? `page:${page.slug}` : 'wiki'} className="space-y-8" {...rise}>
              {!ready ? (
                repo.job ? (
                  <JobProgress job={repo.job} title={`Indexing ${repo.owner}/${repo.name}`} retry={canSync ? sync : undefined} />
                ) : <p className="text-muted-foreground">Not indexed yet.</p> // its first job was never queued, or is gone: Sync queues it
              ) : file ? (
                <FileView repo={repo} path={file} lines={lines} snapshot={fileSnapshot} sha={fileSha} />
              ) : (
                <>
                  {wikiJob && (
                    <JobProgress job={wikiJob} title="Generating the wiki" retry={canSync ? sync : undefined} />
                  )}
                  {page ? (
                    <WikiArticle page={page} repo={repo} writing={wikiBusy} onToc={setToc} onCite={openCitation} />
                  ) : !wikiJob && (
                    <WikiPlaceholder repo={repo} health={healthError ? null : health} wiki={wiki} error={wikiError} onReload={reloadWiki}
                      sync={canSync ? sync : undefined} />
                  )}
                </>
              )}
            </motion.div>
          </AnimatePresence>
        </main>

        <aside className="sticky top-16 hidden h-[calc(100dvh-4rem)] overflow-y-auto px-6 pt-7 pb-36 min-[1240px]:block">
          {syncCard && <div className="mb-7">{syncCard}</div>}
          {!file && page?.content && toc.length > 0 && <Toc items={toc} />}
        </aside>
      </div>
      {repo.permissions.includes('repo.chat') && (
        <AskBox
          placeholder={healthError ? `Questions are unavailable: ${healthError}` : health && !health.chatModel ? 'Set DEEPCODE_CHAT_MODEL to ask questions' : `Ask about ${repo.owner}/${repo.name}`}
          disabled={!ready || !health?.chatModel}
          onAsk={(q) => navigate(`${base}/search`, { state: { question: q } })}
        />
      )}
    </Frame>
  )
}
