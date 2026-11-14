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

type Counts = Pick<UsageRow, 'calls' | 'inputTokens' | 'cachedTokens' | 'outputTokens'>

const add = (a: Counts, b: Counts): Counts => ({
  calls: a.calls + b.calls,
  inputTokens: a.inputTokens + b.inputTokens,
  cachedTokens: a.cachedTokens + b.cachedTokens,
  outputTokens: a.outputTokens + b.outputTokens,
})

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
  const input = (g: UsageRow[]) => g.reduce((n, r) => n + r.inputTokens, 0)
  return [...groups.values()]
    .sort((a, b) => input(b) - input(a))
    .map((g) => g.sort((a, b) => b.inputTokens - a.inputTokens))
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
      {row.inputTokens > 0 && (
        <span className="ml-1.5 text-muted-foreground">{Math.round((100 * row.cachedTokens) / row.inputTokens)}%</span>
      )}
    </>
  )
}

/** The models' token spend per repository: input, the cached part of it, and output, over a chosen span of days. */
export function UsageSection() {
  const rangeId = useId()
  const [days, setDays] = useState(30)
  const { data, error, reload } = useApi(`admin:usage:${days}`, () => listUsage(days))
  const groups = useMemo(() => grouped(data ?? []), [data])
  const total = useMemo(
    () => (data ?? []).reduce(add, { calls: 0, inputTokens: 0, cachedTokens: 0, outputTokens: 0 }),
    [data],
  )

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
        模型伺服器回報的 Token 數。Input 含 Cached Input，也就是伺服器從提示快取讀取的部分；日期以 UTC 計。
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
            </TableRow>
          </TableHeader>
          <TableBody>
            {groups.flatMap((g) =>
              g.map((r, i) => (
                <TableRow key={`${r.repoId}/${r.model}`}>
                  <TableCell>{i === 0 && <Repository row={r} />}</TableCell>
                  <TableCell className="font-mono text-[12.5px]">{r.model}</TableCell>
                  <TableCell className="text-right">{tokens(r.calls)}</TableCell>
                  <TableCell className="text-right">{tokens(r.inputTokens)}</TableCell>
                  <TableCell className="text-right">
                    <Cached row={r} />
                  </TableCell>
                  <TableCell className="text-right">{tokens(r.outputTokens)}</TableCell>
                </TableRow>
              )),
            )}
            {data.length === 0 ? (
              <TableRow>
                <TableCell colSpan={6} className="text-[13px] text-muted-foreground">
                  這段期間沒有模型用量。
                </TableCell>
              </TableRow>
            ) : (
              <TableRow className="border-t font-medium hover:bg-transparent">
                <TableCell colSpan={2}>總計</TableCell>
                <TableCell className="text-right">{tokens(total.calls)}</TableCell>
                <TableCell className="text-right">{tokens(total.inputTokens)}</TableCell>
                <TableCell className="text-right">
                  <Cached row={total} />
                </TableCell>
                <TableCell className="text-right">{tokens(total.outputTokens)}</TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      )}
    </section>
  )
}
