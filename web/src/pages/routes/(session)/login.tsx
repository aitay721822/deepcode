import { createFileRoute } from '@tanstack/react-router'
import { queryParams } from '../../../lib/url'
import { LoginPage } from '../../session/LoginPage'

/** /login?next=: where to go once signed in. */
export const Route = createFileRoute('/(session)/login')({
  validateSearch: (search) => queryParams(search, 'next'),
  component: Login,
})

function Login() {
  return <LoginPage next={Route.useSearch().next} />
}
