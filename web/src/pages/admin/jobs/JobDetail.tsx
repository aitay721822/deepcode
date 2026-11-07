import { Link } from '@tanstack/react-router'
import { ArrowLeftIcon } from 'lucide-react'
import { type ReactNode, useEffect, useRef, useState } from 'react'
import { getJob, watchJobOutput } from '../../../api/admin/jobs'
import type { StreamState } from '../../../api/client'
import { JobProgress } from '../../../components/repo/JobProgress'
import { isActive } from '../../../components/repo/jobStage'
import { LoadError } from '../../../components/ui/feedback/load-error'
import { SkeletonText } from '../../../components/ui/feedback/skeleton'
import { formatTime, shortSha } from '../../../lib/format'
import { useApi } from '../../../lib/useApi'
import type { JobRow, LogLine } from '../../../types/admin/jobs'
import { useSession } from '../../session/session'
import { SectionHead } from '../SectionHead'
import { JobConsole } from './JobConsole'
import { kindLabel, StatusBadge } from './StatusBadge'
import { StreamNotice } from './StreamNotice'

/** One job: what it is, its stages, and its output, followed live until it ends. */
export function JobDetail({ id }: { id: number }) {
  const { refresh } = useSession()
  const { data, error, status, reload } = useApi(`admin:job:${id}`, () => getJob(id))
  const [job, setJob] = useState<JobRow>()
  const [lines, setLines] = useState<LogLine[]>([])
  const last = useRef(0) // the last line shown: a stream, the first or a reconnected one, picks up after it
  const [stream, setStream] = useState<StreamState>()
  const [attempt, setAttempt] = useState(0) // a new attempt opens a new stream
  useEffect(() => {
    if (!data) return
    setJob(data.job)
    setLines(data.logs)
    last.current = data.logs.at(-1)?.id ?? 0
  }, [data])
  useEffect(() => {
    if (!data) return
    // Lines land once per frame: a long output streams in fast, and each render copies them all.
    let queued: LogLine[] = []
    let frame = 0
    const flush = () => {
      cancelAnimationFrame(frame)
      frame = 0
      const batch = queued
      queued = []
      if (batch.length) setLines((ls) => [...ls, ...batch])
    }
    // Opened for finished jobs too: getJob returns only the first lines of a long output, the stream the rest, then `end`.
    const stop = watchJobOutput(
      id,
      last.current,
      {
        log: (l) => {
          if (l.id <= last.current) return // never a line twice
          last.current = l.id
          queued.push(l)
          frame ||= requestAnimationFrame(flush)
        },
        job: setJob,
        end: (j) => {
          setJob(j)
          stop() // the server closes now; left open, EventSource would reconnect
        },
      },
      (s) => {
        setStream(s)
        if (s === 'closed') refresh().catch(() => {}) // a lost session sends the reader to sign in; otherwise the notice stays
      },
    )
    return () => {
      stop()
      flush() // a reconnect resumes after these, so they must not be dropped
    }
  }, [id, data, attempt, refresh])

  const back = (
    <Link
      to="/admin/{-$section}/{-$id}"
      params={{ section: 'jobs', id: undefined }}
      activeOptions={{ exact: true }} // a way back, never the page the reader is on
      className="inline-flex w-fit items-center gap-1.5 text-muted-foreground hover:text-foreground"
    >
      <ArrowLeftIcon className="size-3.5" /> 工作
    </Link>
  )
  if (!data) {
    if (!error) return <SkeletonText />
    return (
      <section className="flex flex-col gap-4">
        {back}
        {status === 404 ? (
          <p className="text-sm text-destructive" role="alert">
            找不到這個工作。
          </p>
        ) : (
          <LoadError error={error} onRetry={reload} />
        )}
      </section>
    )
  }
  const j = job ?? data.job
  return (
    <section className="flex flex-col gap-4">
      {back}
      <SectionHead title={`${j.repoOwner}/${j.repoName}`} className="mb-0">
        <StatusBadge status={j.status} />
      </SectionHead>
      <dl className="flex flex-wrap gap-x-8 gap-y-3 border-y border-dashed py-3.5 text-[13.5px]">
        <Meta term="工作">{j.id}</Meta>
        <Meta term="類型">{kindLabel(j.kind)}</Meta>
        <Meta term="Commit">
          <code className="font-mono">{j.commitSha ? shortSha(j.commitSha) : '-'}</code>
        </Meta>
        <Meta term="嘗試次數">{j.attempts}</Meta>
        <Meta term="建立於">{formatTime(j.createdAt)}</Meta>
        <Meta term="更新於">{formatTime(j.updatedAt)}</Meta>
        <Meta term="Repository">
          <Link to="/repos/$id" params={{ id: j.repoId }} className="text-brand hover:underline">
            開啟 Repository
          </Link>
        </Meta>
      </dl>
      <JobProgress job={j} title={j.kind === 'wiki' ? '產生 Wiki' : '建立索引'} />
      <h2 className="eyebrow mt-3 text-muted-foreground">輸出</h2>
      <div>
        <StreamNotice
          state={stream}
          what="即時輸出"
          onReconnect={() => {
            setStream('retrying')
            setAttempt((a) => a + 1)
          }}
        />
        {/* Only a job still running when opened has live lines: an ended one's long output comes all at once.
            ponytail: a running job opened past getJob's first 5000 lines fades that backlog in too; rare, as a wiki's output is shorter. */}
        <JobConsole lines={lines} liveAfter={isActive(data.job) ? (data.logs.at(-1)?.id ?? 0) : Infinity} />
      </div>
    </section>
  )
}

function Meta({ term, children }: { term: string; children: ReactNode }) {
  return (
    <div>
      <dt className="eyebrow text-muted-foreground">{term}</dt>
      <dd className="mt-1">{children}</dd>
    </div>
  )
}
