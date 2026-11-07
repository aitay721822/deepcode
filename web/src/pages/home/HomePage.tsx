import { useNavigate } from '@tanstack/react-router'
import { ArrowRightIcon, PlusIcon, SearchIcon } from 'lucide-react'
import { motion } from 'motion/react'
import { useRef, useState } from 'react'
import { importRepo, listServers } from '../../api/repo/repos'
import { Button } from '../../components/ui/controls/button'
import { Card } from '../../components/ui/display/card'
import { SkeletonText } from '../../components/ui/feedback/skeleton'
import { Spinner } from '../../components/ui/feedback/spinner'
import { Input } from '../../components/ui/form/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/form/select'
import { hero, item, lift, list, reveal } from '../../lib/style/motion'
import { useApi } from '../../lib/useApi'
import type { Repo } from '../../types/repo/repo'
import { Frame } from '../Frame'
import { useSession } from '../session/session'
import { parseTarget } from './parseTarget'
import { RepoCard } from './RepoCard'
import { useRepos } from './useRepos'

const DEFAULT = 'default' // the select's value for the default server (server ids are numbers)

const matches = (r: Repo, q: string) =>
  `${r.owner}/${r.name} ${r.url} ${r.serverName} ${r.description}`.toLowerCase().includes(q)

export function HomePage() {
  const navigate = useNavigate()
  const { can } = useSession()
  const importer = can('repo.import')
  const { data: repos, error: loadError, reload: reloadRepos } = useRepos()
  const { data: servers, error: serversError } = useApi(importer ? 'servers' : null, listServers)
  const [query, setQuery] = useState('')
  const [serverId, setServerId] = useState('') // '' = the default server
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const input = useRef<HTMLInputElement>(null)

  const target = importer ? parseTarget(query, !!serverId) : null
  const q = query
    .trim()
    .toLowerCase()
    .replace(/\.git$|\/+$/, '')
  const shown = repos?.filter((r) => matches(r, q))

  async function add() {
    if (busy) return // Enter again while adding
    if (!target) return input.current?.focus()
    setBusy(true)
    setError('')
    try {
      const repo = await importRepo(target, serverId ? +serverId : undefined) // an already imported one comes back as is
      navigate({ to: '/repos/$id', params: { id: repo.id } })
    } catch (e) {
      setError((e as Error).message)
      setBusy(false)
    }
  }

  return (
    <Frame>
      <main className="mx-auto max-w-[1070px] px-4 pb-24 min-[900px]:px-6">
        <motion.section
          className="relative isolate pt-14 pb-10 text-center min-[900px]:pt-20"
          variants={hero}
          initial="hidden"
          animate="shown"
        >
          {/* Graph paper, fading out from the middle. */}
          <div
            aria-hidden="true"
            className="dot-grid absolute inset-0 -z-10 [mask-image:radial-gradient(ellipse_at_center,black_15%,transparent_70%)]"
          />
          <motion.h1
            variants={reveal}
            className="mx-auto mb-9 max-w-[780px] font-serif text-[clamp(2.15rem,5.6vw,3.7rem)] leading-[1.04] font-normal tracking-[-0.025em]"
          >
            你想<em className="text-brand">讀懂</em>哪個 Repository？
          </motion.h1>
          <motion.form
            variants={reveal}
            className="mx-auto flex max-w-[640px] flex-wrap justify-center gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              if (target) add()
              else if (shown?.[0]) navigate({ to: '/repos/$id', params: { id: shown[0].id } })
            }}
          >
            <div className="relative max-w-[440px] flex-[1_1_280px]">
              <SearchIcon className="pointer-events-none absolute top-1/2 left-4 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                ref={input}
                value={query}
                autoFocus
                autoCapitalize="none"
                spellCheck={false}
                className="h-12 pl-11 shadow-raised md:text-[15px]"
                aria-label={importer ? '搜尋 Repository，或輸入要新增的 Repository' : '搜尋 Repository'}
                placeholder={importer ? '搜尋，或貼上 Repository 網址' : '搜尋 Repository'}
                onChange={(e) => {
                  setQuery(e.target.value)
                  setError('')
                }}
              />
            </div>
            {servers && servers.length > 1 && (
              <Select value={serverId || DEFAULT} onValueChange={(v) => setServerId(v === DEFAULT ? '' : v)}>
                <SelectTrigger aria-label="要從哪個 Git 伺服器新增" className="h-12 w-[200px] max-w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={DEFAULT}>預設伺服器</SelectItem>
                  {servers.map((s) => (
                    <SelectItem key={s.id} value={String(s.id)}>
                      {s.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </motion.form>
          {serverId && (
            <p className="mt-3 text-xs text-muted-foreground">
              請輸入該伺服器上的專案路徑，例如 group/subgroup/project。
            </p>
          )}
          {serversError && (
            <p className="mt-3 text-xs text-muted-foreground">
              無法列出 Git 伺服器（{serversError}），新的 Repository 會從預設伺服器匯入。
            </p>
          )}
          {error && (
            <p className="mt-4 text-sm text-destructive" role="alert">
              {error}
            </p>
          )}
        </motion.section>
        {loadError && (
          <div className="mb-8 flex flex-col items-center gap-3">
            <p className="text-center text-sm text-destructive" role="alert">
              {loadError}
            </p>
            <Button variant="outline" size="sm" onClick={reloadRepos}>
              重試
            </Button>
          </div>
        )}
        <div className="mb-4 flex items-center gap-3">
          <h2 className="eyebrow text-muted-foreground">Repository</h2>
          <span className="h-px flex-1 bg-border" />
          {q && shown && <span className="eyebrow text-muted-foreground">顯示 {shown.length} 個</span>}
        </div>
        <motion.div
          className="grid grid-cols-[repeat(auto-fill,minmax(290px,1fr))] gap-4"
          variants={list}
          initial="hidden"
          animate="shown"
        >
          {importer && (
            <Card
              asChild
              className="group min-h-40 items-start gap-2 border-dashed border-brand/50 bg-card/40 bg-(image:--add-card) px-4 py-3.5 text-left transition-colors hover:border-brand aria-busy:opacity-60"
            >
              {/* Busy, it ignores presses and gestures but keeps focus, which disabled would drop to the page (as Button's busy). */}
              <motion.button
                type="button"
                variants={item}
                {...(busy ? {} : lift)}
                onClick={busy ? undefined : add}
                aria-disabled={busy || undefined}
                aria-busy={busy || undefined}
              >
                <PlusIcon className="size-5 text-brand" />
                <span className="font-serif text-[21px] leading-snug tracking-[-0.01em] [overflow-wrap:anywhere]">
                  {target ? `新增 ${target.replace(/^https?:\/\//i, '')}` : '新增 Repository'}
                </span>
                <span className="mt-auto flex w-full pt-2.5">
                  <span className="ml-auto grid size-8 place-items-center rounded-full bg-brand text-brand-foreground transition-transform duration-300 group-hover:translate-x-0.5">
                    {busy ? <Spinner /> : <ArrowRightIcon className="size-4" />}
                  </span>
                </span>
              </motion.button>
            </Card>
          )}
          {shown?.map((r) => (
            <RepoCard key={r.id} repo={r} />
          ))}
          {!repos &&
            !loadError &&
            [0, 1, 2].map((i) => (
              <SkeletonText key={i} lines={3} className="min-h-40 content-start rounded-xl border bg-card px-4" />
            ))}
        </motion.div>
        {shown?.length === 0 && !importer && (
          <p className="my-20 text-center font-serif text-[19px] text-muted-foreground italic">
            {q ? '沒有符合的 Repository。' : '還沒有任何 Repository。'}
          </p>
        )}
      </main>
    </Frame>
  )
}
