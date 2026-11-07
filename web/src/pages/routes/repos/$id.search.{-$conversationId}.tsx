import { createFileRoute, notFound } from '@tanstack/react-router'
import { isId, isRepoId } from '../../../lib/url'
import { SearchPage } from '../../repo/search/SearchPage'

/**
 * /repos/:id/search[/:conversationId]: one route for both, so the page stays (and its answer streams on) when a new
 * conversation gets its id.
 */
export const Route = createFileRoute('/repos/$id/search/{-$conversationId}')({
  params: {
    parse: (params) => {
      if (!isRepoId(params.id) || (params.conversationId !== undefined && !isId(params.conversationId)))
        throw notFound()
      return params
    },
  },
  component: Search,
})

function Search() {
  const { id, conversationId } = Route.useParams()
  return <SearchPage id={id} conversationId={conversationId} />
}
