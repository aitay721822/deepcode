import { createFileRoute } from '@tanstack/react-router'
import { queryParams } from '../../lib/url'
import { AdminPage } from '../admin/AdminPage'

/**
 * /admin[/:section[/:id]][?oauth=]: one route, so moving between sections keeps the page (its nav marker slides).
 * AdminPage itself turns down a section it does not have, or one the reader may not see. oauth is how a git server's
 * authorization went, which the server's callback sends back to /admin/servers.
 */
export const Route = createFileRoute('/admin/{-$section}/{-$id}')({
  validateSearch: (search) => queryParams(search, 'oauth'),
  component: Admin,
})

function Admin() {
  const { section, id } = Route.useParams()
  return <AdminPage section={section} id={id} />
}
