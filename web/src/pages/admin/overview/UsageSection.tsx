import { Link } from '@tanstack/react-router'
import { useId, useMemo, useState } from 'react'
import { listUsage } from '../../../api/admin/usage'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../../components/ui/display/table'
import { LoadError } from '../../../components/ui/feedback/load-error'
import { SkeletonText } from '../../../components/ui/feedback/skeleton'
import { Label } from '../../../components/ui/form/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../../components/ui/form/select'
import { useApi } from '../../../lib/useApi'
import type { UsageRow } from '../../../types/admin/usage'
import { SectionHead } from '../SectionHead'

const ranges = [
  { days: 7, label: '7 天' },
  { days: 30, label: '30 天' },
  { days: 0, label: '全部' },
]

const tokens = (n: number) => n.toLocaleString('zh-TW')

/** A span of milliseconds, in the largest units that read well. */
function duration(ms: number) {
  const s = Math.round(ms / 1000)
  if (s < 60) return `${(ms / 1000).toFixed(1)} 秒`
  if (s < 3600) return `${Math.floor(s / 60)} 分 ${s % 60} 秒`
  return `${Math.floor(s / 3600)} 小時 ${Math.floor((s % 3600) / 60)} 分`
}

type Counts = Omit<UsageRow, 'repoId' | 'repoOwner' | 'repoName' | 'model'>

const none: Counts = {
  calls: 0,
  inputTokens: 0,
  cachedTokens: 0,
  outputTokens: 0,
  unreportedCalls: 0,
  estInputTokens: 0,
  estOutputTokens: 0,
  milliseconds: 0,
}

const add = (a: Counts, b: Counts): Counts => ({
  calls: a.calls + b.calls,
  inputTokens: a.inputTokens + b.inputTokens,
  cachedTokens: a.cachedTokens + b.cachedTokens,
  outputTokens: a.outputTokens + b.outputTokens,
  unreportedCalls: a.unreportedCalls + b.unreportedCalls,
  estInputTokens: a.estInputTokens + b.estInputTokens,
  estOutputTokens: a.estOutputTokens + b.estOutputTokens,
  milliseconds: a.milliseconds + b.milliseconds,
})

/** The input a row spent, counted and estimated, which orders the rows. */
const input = (r: Counts) => r.inputTokens + r.estInputTokens

/**
 * The rows grouped by repository, the largest input first, and each repository's models the same way. The
 * others ('', see UsageRow) are one group, each model's rows summed.
 */
function grouped(rows: UsageRow[]) {
  const groups = new Map<string, UsageRow[]>()
  for (const r of rows) {
    const g = groups.get(r.repoId) ?? []
    const same = g.find((o) => o.model === r.model)
    if (same) Object.assign(same, add(same, r))
    else g.push({ ...r })
    groups.set(r.repoId, g)
  }
  const sum = (g: UsageRow[]) => g.reduce((n, r) => n + input(r), 0)
  return [...groups.values()].sort((a, b) => sum(b) - sum(a)).map((g) => g.sort((a, b) => input(b) - input(a)))
}

/** Tokens the server counted, and those estimated for the answers it counted none for, marked so. */
function Tokens({ counted, estimated }: { counted: number; estimated: number }) {
  return (
    <>
      {tokens(counted)}
      {estimated > 0 && (
        <span className="ml-1.5 text-muted-foreground" title="伺服器沒有回報 Token 數的回答，以本地 tokenizer 估算">
          + 約 {tokens(estimated)} 估算
        </span>
      )}
    </>
  )
}

/** The calls, and how many of them the server counted no tokens for. */
function Calls({ row }: { row: Counts }) {
  return (
    <>
      {tokens(row.calls)}
      {row.unreportedCalls > 0 && (
        <span className="ml-1.5 text-muted-foreground" title="伺服器沒有回報 Token 數的呼叫">
          {tokens(row.unreportedCalls)} 未回報
        </span>
      )}
    </>
  )
}

/** The calls' time, summed, and the mean of a call in its title. */
function Time({ row }: { row: Counts }) {
  return (
    <span title={row.calls > 0 ? `平均每次 ${duration(row.milliseconds / row.calls)}` : undefined}>
      {duration(row.milliseconds)}
    </span>
  )
}

/** Its repository, linked; a row of a repository deleted since, or of requests no repository made, says so. */
function Repository({ row }: { row: UsageRow }) {
  if (!row.repoId)
    return (
      <span className="text-muted-foreground" title="已刪除的 Repository，或不屬於任何 Repository 的請求">
        其他
      </span>
    )
  return (
    <Link to="/repos/$id" params={{ id: row.repoId }} className="font-medium hover:underline">
      {row.repoOwner}/{row.repoName}
    </Link>
  )
}

/** Cached input, and its share of the input. */
function Cached({ row }: { row: Counts }) {
  return (
    <>
      {tokens(row.cachedTokens)}
      {row.cachedTokens > 0 && row.inputTokens > 0 && (
        <span className="ml-1.5 text-muted-foreground">{Math.round((100 * row.cachedTokens) / row.inputTokens)}%</span>
      )}
    </>
  )
}

/**
 * The models' spend per repository over a chosen span of days: the calls, input, the cached part of it, output, and
 * time; tokens estimated where the server counted none.
 */
export function UsageSection() {
  const rangeId = useId()
  const [days, setDays] = useState(30)
  const { data, error, reload } = useApi(`admin:usage:${days}`, () => listUsage(days))
  const groups = useMemo(() => grouped(data ?? []), [data])
  const total = useMemo(() => (data ?? []).reduce(add, none), [data])

  return (
    <section>
      <SectionHead title="用量">
        <div className="flex items-center gap-2">
          <Label htmlFor={rangeId} className="font-normal text-muted-foreground">
            期間
          </Label>
          <Select value={String(days)} onValueChange={(v) => setDays(Number(v))}>
            <SelectTrigger id={rangeId} className="h-8 w-28">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {ranges.map((r) => (
                <SelectItem key={r.days} value={String(r.days)}>
                  {r.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </SectionHead>
      <p className="mb-4 text-[13px] text-muted-foreground">
        模型伺服器回報的 Token 數。伺服器沒有回報 Token 數的呼叫（例如訂閱 agent 的回答、被拒絕的請求）記為「未回報」，
        其中有回答的以本地 tokenizer 估算，標示「估算」。Input 含 Cached
        Input，也就是伺服器從提示快取讀取的部分；耗時是每次呼叫的時間總和；日期以 UTC 計。
      </p>
      <LoadError error={error} onRetry={reload} />
      {!data ? (
        !error && <SkeletonText />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Repository</TableHead>
              <TableHead>模型</TableHead>
              <TableHead className="text-right">呼叫次數</TableHead>
              <TableHead className="text-right">Input Token</TableHead>
              <TableHead className="text-right">Cached Input Token</TableHead>
              <TableHead className="text-right">Output Token</TableHead>
              <TableHead className="text-right">耗時</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {groups.flatMap((g) =>
              g.map((r, i) => (
                <TableRow key={`${r.repoId}/${r.model}`}>
                  <TableCell>{i === 0 && <Repository row={r} />}</TableCell>
                  <TableCell className="font-mono text-[12.5px]">{r.model}</TableCell>
                  <TableCell className="text-right">
                    <Calls row={r} />
                  </TableCell>
                  <TableCell className="text-right">
                    <Tokens counted={r.inputTokens} estimated={r.estInputTokens} />
                  </TableCell>
                  <TableCell className="text-right">
                    <Cached row={r} />
                  </TableCell>
                  <TableCell className="text-right">
                    <Tokens counted={r.outputTokens} estimated={r.estOutputTokens} />
                  </TableCell>
                  <TableCell className="text-right">
                    <Time row={r} />
                  </TableCell>
                </TableRow>
              )),
            )}
            {data.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7} className="text-[13px] text-muted-foreground">
                  這段期間沒有模型用量。
                </TableCell>
              </TableRow>
            ) : (
              <TableRow className="border-t font-medium hover:bg-transparent">
                <TableCell colSpan={2}>總計</TableCell>
                <TableCell className="text-right">
                  <Calls row={total} />
                </TableCell>
                <TableCell className="text-right">
                  <Tokens counted={total.inputTokens} estimated={total.estInputTokens} />
                </TableCell>
                <TableCell className="text-right">
                  <Cached row={total} />
                </TableCell>
                <TableCell className="text-right">
                  <Tokens counted={total.outputTokens} estimated={total.estOutputTokens} />
                </TableCell>
                <TableCell className="text-right">
                  <Time row={total} />
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      )}
    </section>
  )
}
