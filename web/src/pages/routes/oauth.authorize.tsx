import { createFileRoute } from '@tanstack/react-router'
import { queryParams } from '../../lib/url'
import { AuthorizePage } from '../session/authorize/AuthorizePage'

/**
 * /oauth/authorize?client_id=&redirect_uri=&response_type=&code_challenge=&code_challenge_method=&state=: the OAuth
 * authorization endpoint, where an app sends the user to approve it (signed in first, like any page).
 */
export const Route = createFileRoute('/oauth/authorize')({
  validateSearch: (search) =>
    queryParams(
      search,
      'client_id',
      'redirect_uri',
      'response_type',
      'code_challenge',
      'code_challenge_method',
      'state',
    ),
  component: Authorize,
})

function Authorize() {
  const s = Route.useSearch()
  return (
    <AuthorizePage
      request={{
        clientId: s.client_id ?? '',
        redirectUri: s.redirect_uri ?? '',
        responseType: s.response_type ?? '',
        codeChallenge: s.code_challenge ?? '',
        codeChallengeMethod: s.code_challenge_method ?? '',
        state: s.state ?? '',
      }}
    />
  )
}
