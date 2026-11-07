import { useState } from 'react'
import { createGitServer, updateGitServer } from '../../../api/admin/servers'
import { Checkbox } from '../../../components/ui/controls/checkbox'
import { Field } from '../../../components/ui/form/field'
import { Input } from '../../../components/ui/form/input'
import { Label } from '../../../components/ui/form/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../../components/ui/form/select'
import { FormDialog } from '../../../components/ui/overlay/form-dialog'
import { plural } from '../../../lib/format'
import type { GitServer } from '../../../types/admin/servers'
import type { Provider } from '../../../types/repo/repo'

/** The API URL the server derives when none is set (gitserver.defaultAPI). */
function derivedApi(kind: Provider, baseUrl: string) {
  const base = baseUrl.trim().replace(/\/+$/, '')
  if (kind === 'gitlab') return `${base}/api/v4`
  return base.toLowerCase() === 'https://github.com' ? 'https://api.github.com' : `${base}/api/v3`
}

/**
 * Creates or edits a git server; an edit sends only what changed. The token is never shown: blank keeps it,
 * the checkbox removes it. Kind and base URL stay fixed while repositories come from the server, and with a
 * stored token the server takes a new address only with the token entered again (its 400 shows in the form).
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
  const [token, setToken] = useState('')
  const [clearToken, setClearToken] = useState(false)
  const repositories = server?.repositoryCount ?? 0
  const inUse = repositories > 0
  const save = async () => {
    const api = apiUrl.trim()
    const base = baseUrl.trim()
    if (server) {
      await updateGitServer(server.id, {
        ...(name !== server.name ? { name } : {}),
        ...(kind !== server.kind ? { kind } : {}),
        ...(base !== server.baseUrl ? { baseUrl: base } : {}),
        ...(api !== override ? { apiUrl: api } : {}), // '' derives it from the base URL again
        ...(clearToken ? { clearToken: true } : token ? { token } : {}),
      })
    } else {
      await createGitServer({ name, kind, baseUrl: base, ...(api ? { apiUrl: api } : {}), ...(token ? { token } : {}) })
    }
    onSaved()
  }
  return (
    <FormDialog
      open={open}
      title={server ? `Edit ${server.name}` : 'New git server'}
      submit={server ? 'Save' : 'Add server'}
      onClose={onClose}
      onSubmit={save}
    >
      <Field label="Name">
        <Input required autoComplete="off" value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label="Kind">
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
        label="Base URL"
        hint={
          inUse
            ? `Used by ${plural(repositories, 'repository', 'repositories')}, so its kind and base URL stay as they are.`
            : server?.hasToken
              ? 'Changing the kind or base URL requires entering the token again.'
              : undefined
        }
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
            API URL <span className="font-normal text-muted-foreground">optional</span>
          </>
        }
        hint={`Only for servers whose API lives elsewhere. Leave it empty to derive it.${server?.hasToken ? ' Changing it requires entering the token again.' : ''}`}
      >
        <Input
          type="url"
          value={apiUrl}
          onChange={(e) => setApiUrl(e.target.value)}
          placeholder={baseUrl.trim() ? derivedApi(kind, baseUrl) : 'Derived from the base URL'}
        />
      </Field>
      <Field
        label={
          <>
            Access token <span className="font-normal text-muted-foreground">optional</span>
          </>
        }
      >
        <Input
          type="password"
          autoComplete="new-password"
          value={token}
          disabled={clearToken}
          onChange={(e) => setToken(e.target.value)}
          placeholder={
            server?.hasToken
              ? 'Leave blank to keep the current token'
              : 'For private repositories and higher rate limits'
          }
        />
      </Field>
      {server?.hasToken && (
        <Label className="font-normal">
          <Checkbox checked={clearToken} onCheckedChange={(c) => setClearToken(c === true)} />
          Remove the stored token
        </Label>
      )}
    </FormDialog>
  )
}
