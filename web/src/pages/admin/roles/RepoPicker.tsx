import { useState } from 'react'
import { ProviderBadge } from '../../../components/repo/provider/ProviderBadge'
import { Checkbox } from '../../../components/ui/controls/checkbox'
import { Input } from '../../../components/ui/form/input'
import { Label } from '../../../components/ui/form/label'
import type { RepoRef } from '../../../types/admin/roles'

/** A searchable multi-select of repositories. */
export function RepoPicker({ repos, value, onChange }: { repos: RepoRef[]; value: number[]; onChange: (ids: number[]) => void }) {
  const [query, setQuery] = useState('')
  const q = query.trim().toLowerCase()
  const shown = repos.filter((r) => `${r.owner}/${r.name} ${r.serverName}`.toLowerCase().includes(q))
  const toggle = (id: number) => onChange(value.includes(id) ? value.filter((x) => x !== id) : [...value, id])
  return (
    <fieldset>
      <legend className="text-sm font-medium">Repositories <span className="font-normal text-muted-foreground">{value.length} selected</span></legend>
      <Input className="mt-2" type="search" placeholder="Filter repositories" aria-label="Filter repositories" value={query} onChange={(e) => setQuery(e.target.value)}
        onKeyDown={(e) => e.key === 'Enter' && !e.nativeEvent.isComposing && e.preventDefault()} />
      <div className="mt-2 grid max-h-56 scroll-py-2 gap-2 overflow-y-auto rounded-lg border px-2.5 py-2">
        {shown.map((r) => (
          <Label key={r.id} className="font-normal">
            <Checkbox checked={value.includes(r.id)} onCheckedChange={() => toggle(r.id)} />
            <span className="min-w-0 [overflow-wrap:anywhere]">{r.owner}/{r.name}</span>
            <ProviderBadge repo={r} />
          </Label>
        ))}
        {shown.length === 0 && <p className="text-[13px] text-muted-foreground">{repos.length ? 'No repository matches.' : 'No repositories yet.'}</p>}
      </div>
    </fieldset>
  )
}
