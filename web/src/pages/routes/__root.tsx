import { createRootRoute, Outlet, useRouterState } from '@tanstack/react-router'
import { motion } from 'motion/react'
import { rise } from '../../lib/style/motion'
import { ErrorPage } from '../ErrorPage'
import { NotFoundPage } from '../NotFoundPage'
import { SessionProvider } from '../session/SessionProvider'

export const Route = createRootRoute({
  component: Root,
  // Unknown paths, and the notFound() a route throws for an id that cannot be one: inside the root, so signed in.
  notFoundComponent: () => <NotFoundPage />,
  errorComponent: ErrorPage,
})

/**
 * Every page waits for the session, then rises in whenever its key changes: home, login, signup, a repository, its
 * search, admin, 404 (not between wiki pages, not when a new conversation gets its id, not between admin sections).
 * Enter only: an exiting <Outlet /> would already show the next page.
 */
function Root() {
  const key = useRouterState({
    select: ({ matches: [root, page] }) =>
      root?._notFound || !page
        ? '404'
        : page.routeId.startsWith('/repos/')
          ? `${page.routeId}:${(page.params as { id: string }).id}`
          : page.routeId,
  })
  return (
    <SessionProvider>
      <motion.div key={key} initial={rise.initial} animate={rise.animate}>
        <Outlet />
      </motion.div>
    </SessionProvider>
  )
}
