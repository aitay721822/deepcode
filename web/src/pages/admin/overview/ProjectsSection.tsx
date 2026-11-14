import { Link, useNavigate } from '@tanstack/react-router'
import { listRepos } from '../../../api/repo/repos'
import { isActive, jobFraction, stageText } from '../../../components/repo/jobStage'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../../components/ui/display/table'
import { LoadError } from '../../../components/ui/feedback/load-error'
import { Progress } from '../../../components/ui/feedback/progress'
import { SkeletonText } from '../../../components/ui/feedback/skeleton'
import { formatTime, shortSha } from '../../../lib/format'
import { cn } from '../../../lib/style/cn'
import { useApi } from '../../../lib/useApi'
import type { Repo } from '../../../types/repo/repo'
import { kindLabel, StatusBadge } from '../jobs/StatusBadge'
import { SectionHead } from '../SectionHead'

const wikiStatus: Record<NonNullable<Repo['snapshot']>['wikiStatus'], string> = {
  none: '尚無',
  generating: '產生中',
  ready: '完成',
  failed: '失敗',
}

const jobLink = (id: number) =>
  ({ to: '/admin/{-$section}/{-$id}', params: { section: 'jobs', id: String(id) } }) as const

/**
 * Every repository the reader may read, as it stands: the commit it serves, its wiki, and its latest job, a job
 * waiting to retry saying why and when. Read again every 5 seconds.
 */
export function ProjectsSection() {
  const navigate = useNavigate()
  const { data, error, reload } = useApi('admin:projects', listRepos, 5000)
  return (
    <section>
      <SectionHead title="專案" />
      <LoadError error={error} onRetry={reload} />
      {!data ? (
        !error && <SkeletonText />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Repository</TableHead>
              <TableHead>目前版本</TableHead>
              <TableHead>Wiki</TableHead>
              <TableHead>最新工作</TableHead>
              <TableHead>更新於</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.map((repo) => {
              const { job, snapshot } = repo
              const name = `${repo.owner}/${repo.name}`
              return (
                <TableRow
                  key={repo.id}
                  className={cn(job && 'cursor-pointer')}
                  onClick={(e) => job && !(e.target as Element).closest('a') && navigate(jobLink(job.id))}
                >
                  <TableCell className="font-medium">
                    <Link to="/repos/$id" params={{ id: repo.id }} className="hover:underline">
                      {name}
                    </Link>
                  </TableCell>
                  <TableCell>
                    {snapshot ? (
                      <>
                        <code className="font-mono">{shortSha(snapshot.commitSha)}</code>
                        <span className="ml-2 text-muted-foreground">{formatTime(snapshot.indexedAt)}</span>
                      </>
                    ) : (
                      <span className="text-muted-foreground">尚未索引</span>
                    )}
                  </TableCell>
                  <TableCell className={cn(snapshot?.wikiStatus === 'failed' && 'text-destructive')}>
                    {snapshot ? wikiStatus[snapshot.wikiStatus] : <span className="text-muted-foreground">—</span>}
                  </TableCell>
                  <TableCell className="min-w-56">
                    {job ? (
                      <Link {...jobLink(job.id)} className="block hover:underline">
                        <span className="flex items-center gap-2">
                          <StatusBadge status={job.status} />
                          <span>{kindLabel(job.kind)}</span>
                        </span>
                        <span
                          className={cn(
                            'mt-1 block max-w-[280px] truncate text-[12.5px]',
                            job.status === 'failed' ? 'text-destructive' : 'text-muted-foreground',
                          )}
                          title={stageText(job)}
                        >
                          {stageText(job)}
                        </span>
                        <Progress
                          className="mt-1.5"
                          value={Math.round(jobFraction(job) * 100)}
                          tone={isActive(job) ? 'warning' : 'muted'}
                          aria-label={`${name} ${kindLabel(job.kind)}`}
                        />
                      </Link>
                    ) : (
                      <span className="text-muted-foreground">—</span>
                    )}
                  </TableCell>
                  <TableCell>{job ? formatTime(job.updatedAt) : ''}</TableCell>
                </TableRow>
              )
            })}
            {data.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="text-[13px] text-muted-foreground">
                  沒有 Repository。
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      )}
    </section>
  )
}
