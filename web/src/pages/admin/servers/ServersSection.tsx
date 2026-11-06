import { PencilIcon, PlusIcon, Trash2Icon } from 'lucide-react'
import { Fragment, useRef, useState } from 'react'
import { deleteGitServer, listGitServers, testGitServer } from '../../../api/admin/servers'
import { ProviderIcon } from '../../../components/repo/provider/ProviderIcon'
import { providerName } from '../../../components/repo/provider/providerName'
import { Button } from '../../../components/ui/controls/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../../components/ui/display/table'
import { SkeletonText } from '../../../components/ui/feedback/skeleton'
import { Spinner } from '../../../components/ui/feedback/spinner'
import { Status } from '../../../components/ui/feedback/status'
import { ConfirmDialog } from '../../../components/ui/overlay/confirm-dialog'
import { plural } from '../../../lib/format'
import { cn } from '../../../lib/style/cn'
import { useApi } from '../../../lib/useApi'
import { useDialog } from '../../../lib/useDialog'
import type { ConnectionTest, GitServer } from '../../../types/admin/servers'
import { LoadError } from '../LoadError'
import { SectionHead } from '../SectionHead'
import { ServerDialog } from './ServerDialog'

/** The GitHub and GitLab servers repositories are imported from. */
export function ServersSection() {
  const { data: servers, error, reload } = useApi('admin:servers', listGitServers)
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
      <SectionHead title="Git servers">
        <Button onClick={() => edit.show('new')}><PlusIcon /> New server</Button>
      </SectionHead>
      <LoadError error={error} onRetry={reload} />
      {!servers ? !error && <SkeletonText /> : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Kind</TableHead>
              <TableHead>Base URL / API URL</TableHead>
              <TableHead>Token</TableHead>
              <TableHead>Repositories</TableHead>
              <TableHead><span className="sr-only">Actions</span></TableHead>
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
                    <TableCell><span className="inline-flex items-center gap-1.5"><ProviderIcon provider={s.kind} className="size-3.5" /> {providerName(s.kind)}</span></TableCell>
                    <TableCell className="font-mono text-[12.5px]">
                      <span className="block max-w-[200px] truncate" title={s.baseUrl}>{s.baseUrl}</span>
                      <span className="block max-w-[200px] truncate text-muted-foreground" title={s.apiUrl}>{s.apiUrl}</span>
                    </TableCell>
                    <TableCell>{s.hasToken ? 'Set' : <span className="text-muted-foreground">None</span>}</TableCell>
                    <TableCell>{s.repositoryCount}</TableCell>
                    <TableCell className="text-right">
                      <Button variant="outline" size="sm" className="mr-1" busy={result === 'busy'} aria-label={`Test connection to ${s.name}`} onClick={() => test(s)}>
                        {result === 'busy' && <Spinner className="size-3" />} Test connection
                      </Button>
                      <Button variant="ghost" size="icon-sm" aria-label={`Edit ${s.name}`} onClick={() => edit.show(s)}><PencilIcon /></Button>
                      <Button variant="ghost" size="icon-sm" aria-label={`Delete ${s.name}`} onClick={() => remove.show(s)}><Trash2Icon /></Button>
                    </TableCell>
                  </TableRow>
                  {done && (
                    // Under its server, across the table. It adds nothing to any column's width (contain), so the table
                    // never grows sideways, and it wraps within what a phone shows of a table that scrolls.
                    <TableRow className="hover:bg-transparent">
                      <TableCell colSpan={6} className="pt-0 whitespace-normal">
                        <Status className={cn('max-w-[calc(100vw-4rem)] text-xs [overflow-wrap:anywhere] [contain:inline-size]', done.ok ? 'text-muted-foreground' : 'text-destructive')}>
                          {done.ok ? 'Connected' : 'Failed'}{done.detail && `: ${done.detail}`}
                        </Status>
                      </TableCell>
                    </TableRow>
                  )}
                </Fragment>
              )
            })}
            {servers.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="text-[13px] text-muted-foreground">No git servers yet.</TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      )}
      {edit.item && (
        <ServerDialog key={edit.key} open={edit.open} server={edit.item === 'new' ? undefined : edit.item} onClose={edit.hide} onSaved={changed} />
      )}
      {removing && (
        <ConfirmDialog key={remove.key} open={remove.open} title={`Delete ${removing.name}?`} confirm="Delete server"
          description={removing.repositoryCount
            ? `Used by ${plural(removing.repositoryCount, 'imported repository', 'imported repositories')}, so it cannot be deleted.`
            : 'No repository uses it. This cannot be undone.'}
          onClose={remove.hide} onConfirm={() => deleteGitServer(removing.id).then(changed)} />
      )}
    </section>
  )
}
