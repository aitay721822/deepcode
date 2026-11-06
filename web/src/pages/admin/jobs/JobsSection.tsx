import { useEffect, useId, useMemo, useState } from 'react'
import { listJobs, watchJobs } from '../../../api/admin/jobs'
import type { StreamState } from '../../../api/client'
import { isActive, jobFraction, stageLabel } from '../../../components/repo/jobStage'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../../components/ui/display/table'
import { Progress } from '../../../components/ui/feedback/progress'
import { SkeletonText } from '../../../components/ui/feedback/skeleton'
import { Label } from '../../../components/ui/form/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../../components/ui/form/select'
import { formatTime } from '../../../lib/format'
import { Link, navigate } from '../../../lib/router'
import { cn } from '../../../lib/style/cn'
import { useApi } from '../../../lib/useApi'
import type { JobRow } from '../../../types/admin/jobs'
import type { Job } from '../../../types/repo/repo'
import { useSession } from '../../session/session'
import { LoadError } from '../LoadError'
import { SectionHead } from '../SectionHead'
import { StatusBadge } from './StatusBadge'
import { StreamNotice } from './StreamNotice'

const statuses: Job['status'][] = ['queued', 'running', 'succeeded', 'failed']
const ALL = 'all' // the filter's value for every status

/** The loaded list with the live changes laid over it (the newer of the two wins), newest job first. */
function merge(loaded: JobRow[], live: Map<number, JobRow>) {
  const rows = new Map(loaded.map((j) => [j.id, j]))
  for (const j of live.values()) {
    const old = rows.get(j.id)
    if (!old || j.updatedAt >= old.updatedAt) rows.set(j.id, j)
  }
  return [...rows.values()].sort((a, b) => b.id - a.id)
}

const stageText = (j: JobRow) => (j.status === 'failed' ? j.error || 'Failed' : j.status === 'succeeded' ? 'Done' : stageLabel(j))

/** Recent jobs, kept live by the job event stream. */
export function JobsSection() {
  const { refresh } = useSession()
  const filterId = useId()
  const [status, setStatus] = useState<Job['status'] | ''>('')
  const { data, error, reload } = useApi(`admin:jobs:${status}`, () => listJobs(status))
  const [live, setLive] = useState(() => new Map<number, JobRow>())
  const [stream, setStream] = useState<StreamState>()
  const [attempt, setAttempt] = useState(0) // a new attempt opens a new stream
  useEffect(() => {
    let dropped = false
    return watchJobs(
      (j) => setLive((m) => new Map(m).set(j.id, j)),
      (s) => {
        setStream(s)
        if (s === 'retrying') dropped = true // EventSource reconnects by itself
        else if (s === 'open' && dropped) {
          dropped = false
          reload() // catch up on what the gap missed
        } else if (s === 'closed') refresh().catch(() => {}) // a lost session sends the reader to sign in; otherwise the notice stays
      },
    )
  }, [attempt])
  const rows = useMemo(() => merge(data ?? [], live).filter((j) => !status || j.status === status), [data, live, status])

  return (
    <section>
      <SectionHead title="Jobs">
        <div className="flex items-center gap-2">
          <Label htmlFor={filterId} className="font-normal text-muted-foreground">Status</Label>
          <Select value={status || ALL} onValueChange={(v) => setStatus(v === ALL ? '' : (v as Job['status']))}>
            <SelectTrigger id={filterId} className="h-8 w-32"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>All</SelectItem>
              {statuses.map((s) => <SelectItem key={s} value={s}>{s}</SelectItem>)}
            </SelectContent>
          </Select>
        </div>
      </SectionHead>
      <StreamNotice state={stream} what="Live updates" onReconnect={() => {
        setStream('retrying')
        setAttempt((a) => a + 1)
        reload()
      }} />
      <LoadError error={error} onRetry={reload} />
      {!data ? !error && <SkeletonText /> : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Status</TableHead>
              <TableHead>Repository</TableHead>
              <TableHead>Kind</TableHead>
              <TableHead>Stage</TableHead>
              <TableHead>Attempts</TableHead>
              <TableHead>Updated</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((j) => (
              <TableRow key={j.id} className="cursor-pointer" onClick={(e) => (e.target as Element).closest('a') || navigate(`/admin/jobs/${j.id}`)}>
                <TableCell><StatusBadge status={j.status} /></TableCell>
                <TableCell className="font-medium"><Link to={`/admin/jobs/${j.id}`} className="hover:underline">{j.repoOwner}/{j.repoName}</Link></TableCell>
                <TableCell>{j.kind}</TableCell>
                <TableCell className="min-w-40">
                  <span className={cn('block max-w-[200px] truncate text-[12.5px]', j.status === 'failed' ? 'text-destructive' : 'text-muted-foreground')} title={stageText(j)}>
                    {stageText(j)}
                  </span>
                  <Progress className="mt-1.5" value={Math.round(jobFraction(j) * 100)} tone={isActive(j) ? 'warning' : 'muted'} aria-label={`${j.repoOwner}/${j.repoName} ${j.kind}`} />
                </TableCell>
                <TableCell>{j.attempts}</TableCell>
                <TableCell>{formatTime(j.updatedAt)}</TableCell>
              </TableRow>
            ))}
            {rows.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="text-[13px] text-muted-foreground">No jobs{status && ` ${status}`}.</TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      )}
    </section>
  )
}
