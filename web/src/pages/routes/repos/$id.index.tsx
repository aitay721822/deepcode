import { createFileRoute, notFound } from '@tanstack/react-router'
import { isRepoId, queryParams } from '../../../lib/url'
import { RepoPage } from '../../repo/RepoPage'

/** /repos/:id[?page=|?file=&L=&s=]: a repository's wiki, or one of its files. */
export const Route = createFileRoute('/repos/$id/')({
  params: {
    parse: (params) => {
      if (!isRepoId(params.id)) throw notFound()
      return params
    },
  },
  validateSearch: (search) => queryParams(search, 'page', 'file', 'L', 's'),
  component: Repo,
})

function Repo() {
  return <RepoPage id={Route.useParams().id} search={Route.useSearch()} />
}
