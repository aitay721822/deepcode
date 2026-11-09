import { Trash2Icon } from 'lucide-react'
import { listApps, revokeApp } from '../../../api/auth'
import { Button } from '../../../components/ui/controls/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../../components/ui/display/table'
import { LoadError } from '../../../components/ui/feedback/load-error'
import { SkeletonText } from '../../../components/ui/feedback/skeleton'
import { ConfirmDialog } from '../../../components/ui/overlay/confirm-dialog'
import { formatDate, formatTime } from '../../../lib/format'
import { useApi } from '../../../lib/useApi'
import { useDialog } from '../../../lib/useDialog'
import type { App } from '../../../types/auth'

/** The apps the user let act for them through OAuth, and when each last did; revoking one stops its tokens. */
export function AppList() {
  const { data: apps, error, reload } = useApi('auth:apps', listApps)
  const revoke = useDialog<App>()
  return (
    <section aria-labelledby="apps-title" className="grid gap-4">
      <h2 id="apps-title" className="font-serif text-[24px] leading-tight font-normal tracking-[-0.015em]">
        已授權的應用程式
      </h2>
      <LoadError error={error} onRetry={reload} />
      {!apps ? (
        !error && <SkeletonText />
      ) : apps.length === 0 ? (
        <p className="text-sm text-muted-foreground">還沒有應用程式透過 OAuth 連線。</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>應用程式</TableHead>
              <TableHead>授權於</TableHead>
              <TableHead>最後使用</TableHead>
              <TableHead>
                <span className="sr-only">操作</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {apps.map((a) => (
              <TableRow key={a.clientId}>
                <TableCell className="font-medium whitespace-normal [overflow-wrap:anywhere]" dir="auto">
                  {a.name}
                </TableCell>
                <TableCell>{formatDate(a.authorizedAt)}</TableCell>
                <TableCell>{a.lastUsedAt ? formatTime(a.lastUsedAt) : '從未使用'}</TableCell>
                <TableCell className="text-right">
                  <Button variant="ghost" size="icon-sm" aria-label={`撤銷 ${a.name}`} onClick={() => revoke.show(a)}>
                    <Trash2Icon />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {revoke.item && (
        <ConfirmDialog
          key={revoke.key}
          open={revoke.open}
          title={`撤銷「${revoke.item.name}」的授權？`}
          confirm="撤銷授權"
          description="這個應用程式將立即無法再存取 deepcode；要再使用，需在應用程式裡重新連線並授權。"
          onClose={revoke.hide}
          onConfirm={() => revokeApp((revoke.item as App).clientId).then(reload)}
        />
      )}
    </section>
  )
}
