import { useEffect, useState } from 'react'
import { listBranches, setBranch as setTrackedBranch } from '../../api/repo/repos'
import { Button } from '../../components/ui/controls/button'
import { Card } from '../../components/ui/display/card'
import { Spinner } from '../../components/ui/feedback/spinner'
import type { Repo } from '../../types/repo/repo'

/** The branch card: the one the repository follows, chosen among the remote's. */
export function BranchCard({ repo, onChanged }: { repo: Repo; onChanged: () => void }) {
  const [branches, setBranches] = useState<string[]>([])
  const [branch, setBranch] = useState(repo.trackedBranch)
  const [busy, setBusy] = useState(false)
  const [note, setNote] = useState('')
  const [error, setError] = useState('')
  const current = repo.trackedBranch

  useEffect(() => {
    let live = true
    setError('')
    setNote('')
    listBranches(repo.id)
      .then((r) => {
        if (!live) return
        setBranches(r.branches)
        setBranch(r.trackedBranch)
      })
      .catch((e) => live && setError(e instanceof Error ? e.message : String(e)))
    return () => {
      live = false
    }
  }, [repo.id])

  async function save() {
    setBusy(true)
    setError('')
    setNote('')
    try {
      const r = await setTrackedBranch(repo.id, branch)
      setNote(r.job ? '已排入同步' : '已是最新的 Commit')
      onChanged()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const options =
    branches.includes(repo.defaultBranch) || !repo.defaultBranch ? branches : [repo.defaultBranch, ...branches]
  return (
    <Card className="gap-0 p-4">
      <p className="font-mono text-[12px] text-muted-foreground">Branch</p>
      <p className="mt-1 text-[12px] [overflow-wrap:anywhere] text-muted-foreground">
        目前跟隨 <span className="text-foreground">{current || repo.defaultBranch || '預設'}</span>
      </p>
      <div className="mt-3 flex items-center gap-2">
        <select
          aria-label="Branch"
          value={branch}
          disabled={busy || options.length === 0}
          onChange={(e) => setBranch(e.target.value)}
          className="h-9 min-w-0 max-w-56 flex-1 rounded-md border border-input bg-card px-3 text-sm transition-colors hover:border-foreground/60 focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/25 focus-visible:outline-hidden"
        >
          <option value="">跟隨預設（{repo.defaultBranch || 'HEAD'}）</option>
          {options.map((b) => (
            <option key={b} value={b}>
              {b}
            </option>
          ))}
        </select>
        <Button variant="outline" size="sm" busy={busy} disabled={branch === current} onClick={save}>
          {busy ? <Spinner /> : null} 切換
        </Button>
      </div>
      {note && (
        <p className="mt-2 text-[13px] text-muted-foreground" role="status">
          {note}
        </p>
      )}
      {error && (
        <p className="mt-2 text-[13px] [overflow-wrap:anywhere] text-destructive" role="alert">
          {error}
        </p>
      )}
    </Card>
  )
}
