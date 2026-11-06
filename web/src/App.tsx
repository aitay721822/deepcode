import { AnimatePresence, MotionConfig, motion } from 'motion/react'
import type { ReactNode } from 'react'
import { decode, isId } from './lib/url'
import { rise } from './lib/style/motion'
import { useLocation } from './lib/router'
import { AdminPage } from './pages/admin/AdminPage'
import { HomePage } from './pages/home/HomePage'
import { ErrorBoundary } from './pages/ErrorBoundary'
import { NotFoundPage } from './pages/NotFoundPage'
import { RepoPage } from './pages/repo/RepoPage'
import { SearchPage } from './pages/search/SearchPage'
import { LoginPage } from './pages/session/LoginPage'
import { SessionProvider } from './pages/session/SessionProvider'
import { SignUpPage } from './pages/session/SignUpPage'

/**
 * Routes, as [key, page]: the key changes only when the page should animate in anew
 * (not between wiki pages, not when a new conversation gets its id, not between admin sections).
 *   /  ·  /login  ·  /signup  ·  /repos/:id[?page=|?file=&L=&s=]  ·  /repos/:id/search[/:conversationId]  ·  /admin[/:section[/:id]]
 */
function route(path: string[], query: URLSearchParams): [string, ReactNode] {
  const [a, b, c, d, ...rest] = path
  if (rest.length) return ['404', <NotFoundPage />]
  if (!a) return ['home', <HomePage />]
  if (a === 'login' && !b) return ['login', <LoginPage next={query.get('next')} />]
  if (a === 'signup' && !b) return ['signup', <SignUpPage next={query.get('next')} />]
  if (a === 'repos' && isId(b) && !c) return [`repo:${b}`, <RepoPage id={+b} params={query} />]
  if (a === 'repos' && isId(b) && c === 'search' && (!d || isId(d))) return [`search:${b}`, <SearchPage id={+b} conversationId={d} />]
  if (a === 'admin' && !d) return ['admin', <AdminPage section={b} id={c} />]
  return ['404', <NotFoundPage />]
}

export function App() {
  const at = useLocation()
  const url = new URL(location.origin + at) // never read "//host" paths as protocol-relative
  const [key, page] = route(url.pathname.split('/').filter(Boolean).map(decode), url.searchParams)
  return (
    <MotionConfig reducedMotion="user">
      <ErrorBoundary at={at}>
        <SessionProvider>
          <AnimatePresence mode="wait">
            <motion.div key={key} {...rise}>{page}</motion.div>
          </AnimatePresence>
        </SessionProvider>
      </ErrorBoundary>
    </MotionConfig>
  )
}
