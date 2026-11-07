import { useState } from 'react'
import { ProviderBadge } from '../../../components/repo/provider/ProviderBadge'
import { Checkbox } from '../../../components/ui/controls/checkbox'
import { Input } from '../../../components/ui/form/input'
import { Label } from '../../../components/ui/form/label'
import type { RepoRef } from '../../../types/admin/roles'

/** How many repositories the picker lists before a search: those updated last. */
const recent = 5

/**
 * A multi-select of repositories: the ones updated last and the ones the role grants (those it granted when the
 * picker opened stay listed while unticked), or, while searching, every match.
 */
export function RepoPicker({
  repos,
  value,
  onChange,
}: {
  repos: RepoRef[]
  value: string[]
  onChange: (ids: string[]) => void
}) {
  const [query, setQuery] = useState('')
  const [granted] = useState(() => new Set(value))
  const q = query.trim().toLowerCase()
  const shown = q
    ? repos.filter((r) => `${r.owner}/${r.name} ${r.serverName}`.toLowerCase().includes(q))
    : [...repos]
        .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt)) // never indexed last
        .filter((r, i) => i < recent || value.includes(r.id) || granted.has(r.id))
  const picked = repos.filter((r) => value.includes(r.id)).length // not ids of repositories gone since
  const toggle = (id: string) => onChange(value.includes(id) ? value.filter((x) => x !== id) : [...value, id])
  return (
    <fieldset>
      <legend className="text-sm font-medium">
        Repository <span className="font-normal text-muted-foreground">已選 {picked} 個</span>
      </legend>
      <Input
        className="mt-2"
        type="search"
        placeholder="搜尋 Repository"
        aria-label="搜尋 Repository"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onKeyDown={(e) => e.key === 'Enter' && !e.nativeEvent.isComposing && e.preventDefault()}
      />
      <div className="mt-2 grid max-h-56 scroll-py-2 gap-2 overflow-y-auto rounded-lg border px-2.5 py-2">
        {shown.map((r) => (
          <Label key={r.id} className="font-normal">
            <Checkbox checked={value.includes(r.id)} onCheckedChange={() => toggle(r.id)} />
            <span className="min-w-0 [overflow-wrap:anywhere]">
              {r.owner}/{r.name}
            </span>
            <ProviderBadge repo={r} />
          </Label>
        ))}
        {shown.length === 0 && (
          <p className="text-[13px] text-muted-foreground">
            {repos.length ? '沒有符合的 Repository。' : '尚無 Repository。'}
          </p>
        )}
      </div>
    </fieldset>
  )
}
