import { PlusIcon, Trash2Icon } from 'lucide-react'
import { useState } from 'react'
import { createToken, listTokens, revokeToken } from '../../../api/auth'
import { Button } from '../../../components/ui/controls/button'
import { Card } from '../../../components/ui/display/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../../components/ui/display/table'
import { LoadError } from '../../../components/ui/feedback/load-error'
import { SkeletonText } from '../../../components/ui/feedback/skeleton'
import { Field } from '../../../components/ui/form/field'
import { Input } from '../../../components/ui/form/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../../components/ui/form/select'
import { ConfirmDialog } from '../../../components/ui/overlay/confirm-dialog'
import { FormDialog } from '../../../components/ui/overlay/form-dialog'
import { formatDate, formatTime } from '../../../lib/format'
import { useApi } from '../../../lib/useApi'
import { useDialog } from '../../../lib/useDialog'
import type { NewToken, Token } from '../../../types/auth'
import { CopyField } from './CopyField'

const lifetimes = [
  { days: '30', label: '30 天' },
  { days: '90', label: '90 天' },
  { days: '365', label: '一年' },
  { days: '0', label: '不過期（直到撤銷）' },
]

/** The user's personal access tokens: create one (its secret shown this once), see when each was last used, revoke. */
export function TokenList() {
  const { data: tokens, error, reload } = useApi('auth:tokens', listTokens)
  const create = useDialog<true>()
  const revoke = useDialog<Token>()
  const [fresh, setFresh] = useState<NewToken | null>(null)
  const [name, setName] = useState('')
  const [days, setDays] = useState('90')
  return (
    <section aria-labelledby="tokens-title" className="grid gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 id="tokens-title" className="font-serif text-[24px] leading-tight font-normal tracking-[-0.015em]">
          個人存取權杖
        </h2>
        <Button
          onClick={() => {
            setName('')
            setDays('90')
            create.show(true)
          }}
        >
          <PlusIcon /> 建立權杖
        </Button>
      </div>
      <p className="text-sm text-muted-foreground">
        權杖以你的身分與角色存取 MCP，只能用在 <code className="font-mono">/mcp</code>
        ，不能登入網頁。請只透過 HTTPS 或本機傳送。
      </p>
      {/* Always there, so a screen reader announces the secret's card when it appears: it is shown this once. */}
      <div aria-live="polite" className="empty:absolute">
        {fresh && (
          <Card className="gap-3 border-brand/40 px-5 py-4">
            <p className="text-sm font-medium">
              「{fresh.token.name}」已建立。請現在複製：關閉此頁後就無法再看到這個權杖。
            </p>
            <CopyField value={fresh.secret} label="權杖" />
            <Button variant="outline" size="sm" className="justify-self-start" onClick={() => setFresh(null)}>
              我已複製
            </Button>
          </Card>
        )}
      </div>
      <LoadError error={error} onRetry={reload} />
      {!tokens ? (
        !error && <SkeletonText />
      ) : tokens.length === 0 ? (
        <p className="text-sm text-muted-foreground">還沒有權杖。</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>名稱</TableHead>
              <TableHead>建立於</TableHead>
              <TableHead>最後使用</TableHead>
              <TableHead>到期</TableHead>
              <TableHead>
                <span className="sr-only">操作</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {tokens.map((t) => (
              <TableRow key={t.id}>
                <TableCell className="font-medium whitespace-normal [overflow-wrap:anywhere]" dir="auto">
                  {t.name}
                </TableCell>
                <TableCell>{formatDate(t.createdAt)}</TableCell>
                <TableCell>{t.lastUsedAt ? formatTime(t.lastUsedAt) : '從未使用'}</TableCell>
                <TableCell>{t.expiresAt ? formatDate(t.expiresAt) : '不過期'}</TableCell>
                <TableCell className="text-right">
                  <Button variant="ghost" size="icon-sm" aria-label={`撤銷 ${t.name}`} onClick={() => revoke.show(t)}>
                    <Trash2Icon />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {create.item && (
        <FormDialog
          key={create.key}
          open={create.open}
          title="建立權杖"
          submit="建立"
          onClose={create.hide}
          onSubmit={async () => {
            setFresh(await createToken(name, Number(days)))
            reload()
          }}
        >
          <Field label="名稱" hint="用來辨認這個權杖，例如使用它的電腦或工具。">
            <Input required maxLength={100} autoComplete="off" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label="有效期限">
            {(control) => (
              <Select value={days} onValueChange={setDays}>
                <SelectTrigger {...control}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {lifetimes.map((l) => (
                    <SelectItem key={l.days} value={l.days}>
                      {l.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </Field>
        </FormDialog>
      )}
      {revoke.item && (
        <ConfirmDialog
          key={revoke.key}
          open={revoke.open}
          title={`撤銷「${revoke.item.name}」？`}
          confirm="撤銷權杖"
          description="使用這個權杖的工具將立即無法連線。此操作無法復原。"
          onClose={revoke.hide}
          onConfirm={async () => {
            const id = (revoke.item as Token).id
            await revokeToken(id)
            setFresh((f) => (f?.token.id === id ? null : f)) // its secret no longer works
            reload()
          }}
        />
      )}
    </section>
  )
}
