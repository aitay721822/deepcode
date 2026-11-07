import { oauthCallbackUrl } from '../../../api/admin/servers'
import { providerName } from '../../../components/repo/provider/providerName'
import { Checkbox } from '../../../components/ui/controls/checkbox'
import { Field } from '../../../components/ui/form/field'
import { Input } from '../../../components/ui/form/input'
import { Label } from '../../../components/ui/form/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../../components/ui/form/select'
import type { GitServer } from '../../../types/admin/servers'
import type { Provider } from '../../../types/repo/repo'

/**
 * How deepcode reads a server's repositories, as the dialog holds it: with a token the admin pastes, or one the
 * OAuth app clientId is granted. clearToken removes the stored token, or ends the authorization.
 */
export type Credentials = {
  method: 'token' | 'oauth'
  token: string
  clientId: string
  clientSecret: string
  clearToken: boolean
}

/** The method server uses now. */
export const storedMethod = (server?: GitServer): Credentials['method'] => (server?.clientId ? 'oauth' : 'token')

/**
 * The credentials part of the git server dialog: the method, then the token, or the OAuth app's client ID and
 * secret with the callback URL to register for it (the authorization itself starts from the server list). Secrets
 * are never shown back: blank keeps them. The checkbox shows only while the method is the stored one, since
 * switching method drops the token anyway.
 */
export function ServerCredentials({
  server,
  kind,
  value,
  onChange,
}: {
  server?: GitServer
  kind: Provider
  value: Credentials
  onChange: (change: Partial<Credentials>) => void
}) {
  const stored = storedMethod(server)
  const keepsSecret = value.method === stored && value.clientId.trim() === server?.clientId && server.hasClientSecret
  return (
    <>
      <Field label="驗證方式">
        {(control) => (
          <Select value={value.method} onValueChange={(v) => onChange({ method: v as Credentials['method'] })}>
            <SelectTrigger {...control} className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="token">存取 Token</SelectItem>
              <SelectItem value="oauth">OAuth 授權</SelectItem>
            </SelectContent>
          </Select>
        )}
      </Field>
      {value.method === 'token' ? (
        <Field
          label={
            <>
              存取 Token <span className="font-normal text-muted-foreground">選填</span>
            </>
          }
        >
          <Input
            type="password"
            autoComplete="new-password"
            value={value.token}
            disabled={value.clearToken}
            onChange={(e) => onChange({ token: e.target.value })}
            placeholder={
              server?.hasToken && stored === 'token' ? '留空則保留目前的 Token' : '用於私有 Repository 及較高的速率限制'
            }
          />
        </Field>
      ) : (
        <>
          <Field
            label="Client ID"
            hint={
              <>
                在 {providerName(kind)} 建立 OAuth 應用程式：回呼網址填{' '}
                <code className="font-mono text-[12px] [overflow-wrap:anywhere]">{oauthCallbackUrl()}</code>
                ，權限範圍選 {kind === 'gitlab' ? 'read_api 與 read_repository' : 'repo'}。儲存後在列表按「授權」。
              </>
            }
          >
            <Input
              required
              autoComplete="off"
              value={value.clientId}
              onChange={(e) => onChange({ clientId: e.target.value })}
            />
          </Field>
          <Field label="Client Secret">
            <Input
              type="password"
              autoComplete="new-password"
              required={!keepsSecret}
              value={value.clientSecret}
              onChange={(e) => onChange({ clientSecret: e.target.value })}
              placeholder={keepsSecret ? '留空則保留目前的 Client Secret' : undefined}
            />
          </Field>
        </>
      )}
      {server?.hasToken && value.method === stored && (
        <Label className="font-normal">
          <Checkbox checked={value.clearToken} onCheckedChange={(c) => onChange({ clearToken: c === true })} />
          {stored === 'oauth' ? '取消授權（移除授權取得的 Token）' : '移除已儲存的 Token'}
        </Label>
      )}
    </>
  )
}
