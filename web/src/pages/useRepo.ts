import { useEffect, useState } from 'react'
import { getRepo } from '../api/repo/repos'
import { awaitingWiki, isActive } from '../components/repo/jobStage'
import { useApi } from '../lib/useApi'

/**
 * One repository (404 when it does not exist or the reader may not see it); polled while its job runs,
 * and a few times more after indexing while its wiki job may still be on the way.
 * A 404 drops what was loaded and ends the polling: deleted, or no longer visible, it does not come back.
 */
export function useRepo(id: string) {
  const [polls, setPolls] = useState<number | null>(null) // left to make (null: not counted yet); a running job refills them
  const res = useApi(`repo:${id}`, () => getRepo(id), polls ? 1500 : undefined)
  const gone = res.status === 404
  const repo = gone ? undefined : res.data
  useEffect(() => {
    if (gone) setPolls(0)
    else if (repo) setPolls((n) => (isActive(repo.job) ? 3 : awaitingWiki(repo) ? Math.max(0, (n ?? 3) - 1) : 0))
  }, [repo, gone])
  return { ...res, data: repo }
}
