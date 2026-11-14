import { cn } from '../../lib/style/cn'
import type { Job } from '../../types/repo/repo'
import { Button } from '../ui/controls/button'
import { Card } from '../ui/display/card'
import { Progress } from '../ui/feedback/progress'
import { Spinner } from '../ui/feedback/spinner'
import { Status } from '../ui/feedback/status'
import { isActive, jobFraction, retrying, retryText, STAGES, stageIndex } from './jobStage'

const stepTone = {
  done: "text-muted-foreground before:text-success before:content-['✓']",
  now: "font-medium text-foreground before:text-brand before:content-['›']",
  todo: "text-muted-foreground before:content-['']",
}

/**
 * A job's stages and progress; one waiting to retry says why and when, with its last error; a failed one shows its
 * error and, given retry, a Retry button and what came of it.
 */
export function JobProgress({
  job,
  title,
  retry,
}: {
  job: Job
  title: string
  retry?: { run: () => void; busy: boolean; error: string; upToDate: boolean }
}) {
  const stages = STAGES[job.kind]
  const at = stageIndex(job)
  const failed = job.status === 'failed'
  return (
    <Card asChild className="gap-4 px-5 py-[18px]">
      <section>
        <div className="flex items-center gap-2.5" role="status">
          <span className="font-serif text-[20px] leading-tight [overflow-wrap:anywhere]">
            {/* A space only after a Latin letter or digit: "產生 Wiki 失敗", "建立索引失敗". */}
            {failed ? `${title}${/[A-Za-z0-9]$/.test(title) ? ' ' : ''}失敗` : title}
          </span>
          {isActive(job) && (
            <span className="eyebrow ml-auto text-muted-foreground">
              {at + 1} / {stages.length}
            </span>
          )}
        </div>
        <Progress
          value={Math.round(jobFraction(job) * 100)}
          tone={isActive(job) ? 'warning' : 'muted'}
          aria-label={title}
        />
        <ul className="font-mono text-[12.5px]">
          {stages.map(([key, label], i) => {
            const state = job.status === 'succeeded' || i < at ? 'done' : i === at && !failed ? 'now' : 'todo'
            return (
              <li
                key={key}
                className={cn(
                  "flex items-center gap-2.5 py-1 before:w-3 after:min-w-6 after:flex-1 after:border-b after:border-dashed after:content-['']",
                  stepTone[state],
                )}
              >
                <span>{label}</span>
                {state === 'now' && job.total > 0 && (
                  <span className="order-2 font-normal text-muted-foreground tabular-nums">
                    {job.progress} / {job.total}
                  </span>
                )}
              </li>
            )
          })}
        </ul>
        {retrying(job) && (
          <div className="mt-1.5 space-y-1.5 text-sm [overflow-wrap:anywhere]">
            <p>{retryText(job)}</p>
            {job.error && <p className="text-muted-foreground">{job.error}</p>}
          </div>
        )}
        {failed && (
          <div className="flex items-start justify-between gap-3 text-sm">
            <div className="mt-1.5 space-y-1.5 [overflow-wrap:anywhere]">
              <p className="text-destructive">{job.error}</p>
              {retry?.error && (
                <p className="text-destructive" role="alert">
                  重試失敗：{retry.error}
                </p>
              )}
              {retry?.upToDate && <Status className="text-muted-foreground">已是最新的 Commit</Status>}
            </div>
            {retry && (
              <Button variant="outline" size="sm" busy={retry.busy} onClick={retry.run}>
                {retry.busy && <Spinner />} 重試
              </Button>
            )}
          </div>
        )}
      </section>
    </Card>
  )
}
