import { useState } from 'react'
import { createGitServer, updateGitServer } from '../../../api/admin/servers'
import { Field } from '../../../components/ui/form/field'
import { Input } from '../../../components/ui/form/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../../components/ui/form/select'
import { FormDialog } from '../../../components/ui/overlay/form-dialog'
import { plural } from '../../../lib/format'
import type { GitServer } from '../../../types/admin/servers'
import type { Provider } from '../../../types/repo/repo'
import { type Credentials, ServerCredentials, storedMethod } from './ServerCredentials'

/** The API URL the server derives when none is set (gitserver.defaultAPI). */
function derivedApi(kind: Provider, baseUrl: string) {
  const base = baseUrl.trim().replace(/\/+$/, '')
  if (kind === 'gitlab') return `${base}/api/v4`
  return base.toLowerCase() === 'https://github.com' ? 'https://api.github.com' : `${base}/api/v3`
}

/**
 * Creates or edits a git server; an edit sends only what changed. Its credentials are a pasted token or an OAuth
 * app (ServerCredentials), never shown back. Kind and base URL stay fixed while repositories come from the server,
 * and a stored token or client secret goes to a new address only when entered again (its 400 shows in the form).
 * The API URL field holds only an override; the derived URL is its placeholder and follows kind and base URL.
 */
export function ServerDialog({
  open,
  server,
  onClose,
  onSaved,
}: {
  open: boolean
  server?: GitServer
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState(server?.name ?? '')
  const [kind, setKind] = useState<Provider>(server?.kind ?? 'github')
  const [baseUrl, setBaseUrl] = useState(server?.baseUrl ?? '')
  // The server reports the effective API URL: one that equals the derived URL is no override.
  const override = server && server.apiUrl !== derivedApi(server.kind, server.baseUrl) ? server.apiUrl : ''
  const [apiUrl, setApiUrl] = useState(override)
  const [creds, setCreds] = useState<Credentials>({
    method: storedMethod(server),
    token: '',
    clientId: server?.clientId ?? '',
    clientSecret: '',
    clearToken: false,
  })
  const repositories = server?.repositoryCount ?? 0
  const inUse = repositories > 0
  const save = async () => {
    const api = apiUrl.trim()
    const base = baseUrl.trim()
    const oauth = creds.method === 'oauth'
    const id = oauth ? creds.clientId.trim() : '' // '' goes back to a pasted token
    const credentials = {
      ...(oauth && creds.clientSecret ? { clientSecret: creds.clientSecret } : {}),
      ...(!oauth && creds.token ? { token: creds.token } : {}),
    }
    if (server) {
      await updateGitServer(server.id, {
        ...(name !== server.name ? { name } : {}),
        ...(kind !== server.kind ? { kind } : {}),
        ...(base !== server.baseUrl ? { baseUrl: base } : {}),
        ...(api !== override ? { apiUrl: api } : {}), // '' derives it from the base URL again
        ...(id !== server.clientId ? { clientId: id } : {}),
        ...(creds.clearToken && creds.method === storedMethod(server) ? { clearToken: true } : credentials),
      })
    } else {
      await createGitServer({
        name,
        kind,
        baseUrl: base,
        ...(api ? { apiUrl: api } : {}),
        ...(id ? { clientId: id } : {}),
        ...credentials,
      })
    }
    onSaved()
  }
  const moving = server?.clientId
    ? '變更類型或基底網址時，須重新輸入 Client Secret 並重新授權。'
    : server?.hasToken
      ? '變更類型或基底網址時，須重新輸入 Token。'
      : undefined
  return (
    <FormDialog
      open={open}
      title={server ? `編輯 ${server.name}` : '新增 Git 伺服器'}
      submit={server ? '儲存' : '新增伺服器'}
      onClose={onClose}
      onSubmit={save}
    >
      <Field label="名稱">
        <Input required autoComplete="off" value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label="類型">
        {(control) => (
          <Select value={kind} disabled={inUse} onValueChange={(v) => setKind(v as Provider)}>
            <SelectTrigger {...control} className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="github">GitHub</SelectItem>
              <SelectItem value="gitlab">GitLab</SelectItem>
            </SelectContent>
          </Select>
        )}
      </Field>
      <Field
        label="基底網址"
        hint={inUse ? `有 ${plural(repositories, '個 Repository')} 使用此伺服器，因此類型與基底網址無法變更。` : moving}
      >
        <Input
          type="url"
          required
          value={baseUrl}
          disabled={inUse}
          onChange={(e) => setBaseUrl(e.target.value)}
          placeholder={kind === 'gitlab' ? 'https://gitlab.example.com' : 'https://github.com'}
        />
      </Field>
      <Field
        label={
          <>
            API URL <span className="font-normal text-muted-foreground">選填</span>
          </>
        }
        hint={`僅適用於 API 位於他處的伺服器。留空則自動推導。${moving ? moving.replace('變更類型或基底網址時', '變更時') : ''}`}
      >
        <Input
          type="url"
          value={apiUrl}
          onChange={(e) => setApiUrl(e.target.value)}
          placeholder={baseUrl.trim() ? derivedApi(kind, baseUrl) : '由基底網址推導'}
        />
      </Field>
      <ServerCredentials
        server={server}
        kind={kind}
        value={creds}
        onChange={(change) => setCreds((c) => ({ ...c, ...change }))}
      />
    </FormDialog>
  )
}
