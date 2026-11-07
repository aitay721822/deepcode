import { AnimatePresence, MotionConfig, motion } from 'motion/react'
import type { ReactNode } from 'react'
import { useLocation } from './lib/router'
import { rise } from './lib/style/motion'
import { decode, isId, isRepoId } from './lib/url'
import { AdminPage } from './pages/admin/AdminPage'
import { ErrorBoundary } from './pages/ErrorBoundary'
import { HomePage } from './pages/home/HomePage'
import { NotFoundPage } from './pages/NotFoundPage'
import { RepoPage } from './pages/repo/RepoPage'
import { SearchPage } from './pages/search/SearchPage'
import { LoginPage } from './pages/session/LoginPage'
import { SessionProvider } from './pages/session/SessionProvider'
import { SignUpPage } from './pages/session/SignUpPage'

/**
 * Routes, as { key, page }: the key changes only when the page should animate in anew
 * (not between wiki pages, not when a new conversation gets its id, not between admin sections).
 *   /  ·  /login  ·  /signup  ·  /repos/:id[?page=|?file=&L=&s=]  ·  /repos/:id/search[/:conversationId]  ·  /admin[/:section[/:id]]
 */
function route(path: string[], query: URLSearchParams): { key: string; page: ReactNode } {
  const [a, b, c, d, ...rest] = path
  if (rest.length) return { key: '404', page: <NotFoundPage /> }
  if (!a) return { key: 'home', page: <HomePage /> }
  if (a === 'login' && !b) return { key: 'login', page: <LoginPage next={query.get('next')} /> }
  if (a === 'signup' && !b) return { key: 'signup', page: <SignUpPage next={query.get('next')} /> }
  if (a === 'repos' && isRepoId(b) && !c) return { key: `repo:${b}`, page: <RepoPage id={b} params={query} /> }
  if (a === 'repos' && isRepoId(b) && c === 'search' && (!d || isId(d))) {
    return { key: `search:${b}`, page: <SearchPage id={b} conversationId={d} /> }
  }
  if (a === 'admin' && !d) return { key: 'admin', page: <AdminPage section={b} id={c} /> }
  return { key: '404', page: <NotFoundPage /> }
}

export function App() {
  const at = useLocation()
  const url = new URL(location.origin + at) // never read "//host" paths as protocol-relative
  const { key, page } = route(url.pathname.split('/').filter(Boolean).map(decode), url.searchParams)
  return (
    <MotionConfig reducedMotion="user">
      <ErrorBoundary at={at}>
        <SessionProvider>
          <AnimatePresence mode="wait">
            <motion.div key={key} {...rise}>
              {page}
            </motion.div>
          </AnimatePresence>
        </SessionProvider>
      </ErrorBoundary>
    </MotionConfig>
  )
}
