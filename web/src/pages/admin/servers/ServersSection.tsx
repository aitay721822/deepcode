import { useNavigate, useSearch } from '@tanstack/react-router'
import { PencilIcon, PlusIcon, Trash2Icon } from 'lucide-react'
import { Fragment, useEffect, useRef, useState } from 'react'
import { deleteGitServer, gitServerAuthorizeUrl, listGitServers, testGitServer } from '../../../api/admin/servers'
import { ProviderIcon } from '../../../components/repo/provider/ProviderIcon'
import { providerName } from '../../../components/repo/provider/providerName'
import { Button } from '../../../components/ui/controls/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../../components/ui/display/table'
import { LoadError } from '../../../components/ui/feedback/load-error'
import { SkeletonText } from '../../../components/ui/feedback/skeleton'
import { Spinner } from '../../../components/ui/feedback/spinner'
import { Status } from '../../../components/ui/feedback/status'
import { ConfirmDialog } from '../../../components/ui/overlay/confirm-dialog'
import { plural } from '../../../lib/format'
import { cn } from '../../../lib/style/cn'
import { useApi } from '../../../lib/useApi'
import { useDialog } from '../../../lib/useDialog'
import type { ConnectionTest, GitServer } from '../../../types/admin/servers'
import { SectionHead } from '../SectionHead'
import { ServerDialog } from './ServerDialog'

/**
 * The GitHub and GitLab servers repositories are imported from. An OAuth authorization comes back here with
 * ?oauth=ok or what went wrong: shown once, then taken off the URL so a reload does not show it again.
 */
export function ServersSection() {
  const { data: servers, error, reload } = useApi('admin:servers', listGitServers)
  const { oauth } = useSearch({ from: '/admin/{-$section}/{-$id}' })
  const [authorized] = useState(oauth)
  const navigate = useNavigate()
  useEffect(() => {
    if (oauth)
      navigate({
        to: '/admin/{-$section}/{-$id}',
        params: { section: 'servers', id: undefined },
        search: {},
        replace: true,
        resetScroll: false,
      })
  }, [oauth, navigate])
  const edit = useDialog<GitServer | 'new'>()
  const remove = useDialog<GitServer>()
  const removing = remove.item
  const [tests, setTests] = useState<Record<number, ConnectionTest | 'busy'>>({})
  const epoch = useRef(0) // counts changes: a test started before one is about a server that is no more
  const test = async (s: GitServer) => {
    const started = epoch.current
    setTests((t) => ({ ...t, [s.id]: 'busy' }))
    const result = await testGitServer(s.id).catch((e: Error) => ({ ok: false, detail: e.message }))
    if (epoch.current === started) setTests((t) => ({ ...t, [s.id]: result }))
  }
  const changed = () => {
    epoch.current++
    setTests({}) // a result says nothing about a server since edited, or a new one given a deleted one's id
    reload()
  }
  return (
    <section>
      <SectionHead title="Git 伺服器">
        <Button onClick={() => edit.show('new')}>
          <PlusIcon /> 新增伺服器
        </Button>
      </SectionHead>
      <LoadError error={error} onRetry={reload} />
      {authorized && (
        <Status className={cn('mb-3 text-[13px]', authorized === 'ok' ? 'text-muted-foreground' : 'text-destructive')}>
          {authorized === 'ok' ? '已完成授權。' : `授權失敗：${authorized}`}
        </Status>
      )}
      {!servers ? (
        !error && <SkeletonText />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>名稱</TableHead>
              <TableHead>類型</TableHead>
              <TableHead>基底網址 / API URL</TableHead>
              <TableHead>Token</TableHead>
              <TableHead>Repository</TableHead>
              <TableHead>
                <span className="sr-only">操作</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {servers.map((s) => {
              const result = tests[s.id]
              const done = result === 'busy' ? undefined : result
              return (
                <Fragment key={s.id}>
                  <TableRow className={cn(done && 'border-b-0')}>
                    <TableCell className="font-medium">{s.name}</TableCell>
                    <TableCell>
                      <span className="inline-flex items-center gap-1.5">
                        <ProviderIcon provider={s.kind} className="size-3.5" /> {providerName(s.kind)}
                      </span>
                    </TableCell>
                    <TableCell className="font-mono text-[12.5px]">
                      <span className="block max-w-[200px] truncate" title={s.baseUrl}>
                        {s.baseUrl}
                      </span>
                      <span className="block max-w-[200px] truncate text-muted-foreground" title={s.apiUrl}>
                        {s.apiUrl}
                      </span>
                    </TableCell>
                    <TableCell>
                      {s.clientId ? (
                        s.hasToken ? (
                          'OAuth 已授權'
                        ) : (
                          <span className="text-muted-foreground">OAuth 未授權</span>
                        )
                      ) : s.hasToken ? (
                        '已設定'
                      ) : (
                        <span className="text-muted-foreground">無</span>
                      )}
                    </TableCell>
                    <TableCell>{s.repositoryCount}</TableCell>
                    <TableCell className="text-right">
                      {s.clientId && (
                        // A page load, not a fetch: our server sends the browser on to the consent page.
                        <Button
                          variant="outline"
                          size="sm"
                          className="mr-1"
                          aria-label={`以 ${providerName(s.kind)} 授權 ${s.name}`}
                          onClick={() => location.assign(gitServerAuthorizeUrl(s.id))}
                        >
                          {s.hasToken ? '重新授權' : '授權'}
                        </Button>
                      )}
                      <Button
                        variant="outline"
                        size="sm"
                        className="mr-1"
                        busy={result === 'busy'}
                        aria-label={`測試與 ${s.name} 的連線`}
                        onClick={() => test(s)}
                      >
                        {result === 'busy' && <Spinner className="size-3" />} 測試連線
                      </Button>
                      <Button variant="ghost" size="icon-sm" aria-label={`編輯 ${s.name}`} onClick={() => edit.show(s)}>
                        <PencilIcon />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`刪除 ${s.name}`}
                        onClick={() => remove.show(s)}
                      >
                        <Trash2Icon />
                      </Button>
                    </TableCell>
                  </TableRow>
                  {done && (
                    // Under its server, across the table. It adds nothing to any column's width (contain), so the table
                    // never grows sideways, and it wraps within what a phone shows of a table that scrolls.
                    <TableRow className="hover:bg-transparent">
                      <TableCell colSpan={6} className="pt-0 whitespace-normal">
                        <Status
                          className={cn(
                            'max-w-[calc(100vw-4rem)] text-xs [overflow-wrap:anywhere] [contain:inline-size]',
                            done.ok ? 'text-muted-foreground' : 'text-destructive',
                          )}
                        >
                          {done.ok ? '已連線' : '失敗'}
                          {done.detail && `：${done.detail}`}
                        </Status>
                      </TableCell>
                    </TableRow>
                  )}
                </Fragment>
              )
            })}
            {servers.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="text-[13px] text-muted-foreground">
                  尚無 Git 伺服器。
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      )}
      {edit.item && (
        <ServerDialog
          key={edit.key}
          open={edit.open}
          server={edit.item === 'new' ? undefined : edit.item}
          onClose={edit.hide}
          onSaved={changed}
        />
      )}
      {removing && (
        <ConfirmDialog
          key={remove.key}
          open={remove.open}
          title={`刪除 ${removing.name}？`}
          confirm="刪除伺服器"
          description={
            removing.repositoryCount
              ? `有 ${plural(removing.repositoryCount, '個已匯入的 Repository')} 使用此伺服器，因此無法刪除。`
              : '沒有 Repository 使用此伺服器。此操作無法復原。'
          }
          onClose={remove.hide}
          onConfirm={() => deleteGitServer(removing.id).then(changed)}
        />
      )}
    </section>
  )
}
