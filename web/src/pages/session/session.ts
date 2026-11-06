import { createContext, useContext } from 'react'
import type { Me, Permission } from '../../types/auth'

type Session = {
  user: Me | null // null only on the sign-in page
  can: (p: Permission) => boolean
  logout: () => Promise<void>
  refresh: () => Promise<void> // rejects when the server cannot say who is signed in (a 401 signs out instead)
}

export const SessionContext = createContext<Session | null>(null)

/**
 * Where to go once signed in: a path on this site, never another origin (?next=//evil.example, or /.//evil.example,
 * whose path becomes //evil.example), nor back to signing in or up.
 */
export function destination(next: string | null) {
  try {
    const u = new URL(next || '/', location.origin)
    if (u.origin === location.origin && !['/login', '/signup'].includes(u.pathname) && !u.pathname.startsWith('//')) return u.pathname + u.search + u.hash
  } catch {
    // malformed: go home
  }
  return '/'
}

/** path, keeping where to go once signed in: between the sign-in and sign-up pages. */
export const withNext = (path: string, next: string | null) => (next ? `${path}?next=${encodeURIComponent(next)}` : path)

export function useSession() {
  const s = useContext(SessionContext)
  if (!s) throw new Error('useSession outside SessionProvider')
  return s
}
