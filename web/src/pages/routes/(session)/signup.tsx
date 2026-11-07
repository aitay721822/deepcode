import { createFileRoute } from '@tanstack/react-router'
import { queryParams } from '../../../lib/url'
import { SignUpPage } from '../../session/SignUpPage'

/** /signup?next=: where to go once signed up. */
export const Route = createFileRoute('/(session)/signup')({
  validateSearch: (search) => queryParams(search, 'next'),
  component: SignUp,
})

function SignUp() {
  return <SignUpPage next={Route.useSearch().next} />
}
