import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { getMe, logout as signOut } from '../../api/auth'
import { setUnauthorizedHandler } from '../../api/client'
import { Notice } from '../../components/layout/Notice'
import { Button } from '../../components/ui/controls/button'
import { SkeletonText } from '../../components/ui/feedback/skeleton'
import { navigate, useLocation } from '../../lib/router'
import type { Me, Permission } from '../../types/auth'
import { SessionContext } from './session'

/**
 * Loads the signed-in user (GET /api/auth/me) and shares it. Every page but /login and /signup waits for it;
 * without a session (no cookie, a 401 later, signing out) the reader goes to /login?next=<here>.
 * Only the first load's failure stands in for the app: a later one keeps the user the app shows.
 */
export function SessionProvider({ children }: { children: ReactNode }) {
  const path = useLocation()
  const publicPage = ['/login', '/signup'].includes(path.split('?')[0]) // pages for the signed out
  const [user, setUser] = useState<Me | null>() // undefined while loading
  const [error, setError] = useState('') // the first load failed
  // A 401 signs out; any other failure rejects and leaves the user as it was.
  const refresh = useCallback(async () => {
    try {
      setUser(await getMe())
    } catch (e) {
      if ((e as { status?: number }).status !== 401) throw e
      setUser(null)
    }
  }, [])
  const load = () => {
    setError('')
    refresh().catch((e: Error) => setError(e.message))
  }
  useEffect(() => {
    setUnauthorizedHandler(() => setUser(null))
    load()
  }, [])
  useEffect(() => {
    if (user === null && !publicPage) navigate(`/login?next=${encodeURIComponent(path)}`, { replace: true })
  }, [user, publicPage])
  const session = useMemo(() => ({
    user: user ?? null,
    can: (p: Permission) => !!user?.permissions.includes(p),
    refresh,
    logout: async () => {
      await signOut().catch(() => {}) // signed out here either way
      setUser(null)
      navigate('/login')
    },
  }), [user, refresh])

  let body = children
  if (!publicPage && !user) {
    // Loading (quietly) or failed (with a Retry); null: on the way to /login.
    body = user === null ? null : (
      <main>
        <Notice>
          {error ? (
            <>
              <p className="text-sm text-destructive" role="alert">{error}</p>
              <Button variant="outline" onClick={load}>Retry</Button>
            </>
          ) : <SkeletonText />}
        </Notice>
      </main>
    )
  }
  return <SessionContext value={session}>{body}</SessionContext>
}
