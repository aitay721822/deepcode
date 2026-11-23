import { createFileRoute, notFound } from '@tanstack/react-router'
import { isId, queryParams } from '../../../lib/url'
import { RepoPage } from '../../repo/RepoPage'

/** /repos/:id: a repository's wiki (?page=), or one of its files (?file= with optional page, L and s). */
export const Route = createFileRoute('/repos/$id/')({
  params: {
    parse: (params) => {
      if (!isId(params.id)) throw notFound()
      return params
    },
  },
  validateSearch: (search) => queryParams(search, 'page', 'file', 'L', 's'),
  component: Repo,
})

function Repo() {
  return <RepoPage id={Route.useParams().id} search={Route.useSearch()} />
}
