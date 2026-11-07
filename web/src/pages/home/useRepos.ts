import { useEffect, useState } from 'react'
import { listRepos } from '../../api/repo/repos'
import { awaitingWiki, isActive } from '../../components/repo/jobStage'
import { useApi } from '../../lib/useApi'

/** The repositories the reader may see; polled while any job runs, and a few times more while a wiki job may be on the way. */
export function useRepos() {
  const [polls, setPolls] = useState<number | null>(null) // left to make (null: not counted yet); a running job refills them
  const res = useApi('repos', listRepos, polls ? 1500 : undefined)
  const repos = res.data
  useEffect(() => {
    if (repos)
      setPolls((n) =>
        repos.some((r) => isActive(r.job)) ? 3 : repos.some(awaitingWiki) ? Math.max(0, (n ?? 3) - 1) : 0,
      )
  }, [repos])
  return res
}
