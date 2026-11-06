/**
 * What the add box would import, as the `url` POST /api/repos takes; null when the input is a search instead.
 * A web URL (https://host/group/sub/project), owner/name on the default server, or, once a server is
 * picked, any group/sub/project path on it. The server has the last word on what exists.
 */
export function parseTarget(input: string, serverPicked: boolean) {
  const s = input.trim()
  if (/^https?:\/\//i.test(s)) {
    try {
      return new URL(s).pathname.split('/').filter(Boolean).length >= 2 ? s : null
    } catch {
      return null
    }
  }
  return (serverPicked ? /^[\w.-]+(\/[\w.-]+)+$/ : /^[\w.-]+\/[\w.-]+$/).test(s) ? s : null
}
