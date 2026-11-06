import { RefreshCwIcon } from 'lucide-react'
import type { StreamState } from '../../../api/client'
import { Button } from '../../../components/ui/controls/button'
import { Spinner } from '../../../components/ui/feedback/spinner'
import { Status } from '../../../components/ui/feedback/status'

/**
 * A live stream that is not simply live: dropped and reconnecting on its own, or stopped
 * (signed out, access lost, server gone), with a button that tries once more.
 */
export function StreamNotice({ state, what, onReconnect }: { state?: StreamState; what: string; onReconnect: () => void }) {
  if (state === 'retrying') {
    return <Status className="mb-3 flex items-center gap-2 text-[12.5px] text-muted-foreground"><Spinner className="size-3" /> {what} interrupted, reconnecting…</Status>
  }
  if (state !== 'closed') return null
  return (
    <Status className="mb-3 flex flex-wrap items-center gap-2 text-[12.5px] text-muted-foreground">
      {what} stopped.
      <Button variant="outline" size="sm" className="h-7" onClick={onReconnect}><RefreshCwIcon className="size-3.5" /> Reconnect</Button>
    </Status>
  )
}
