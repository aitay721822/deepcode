import { createRouter, RouterProvider, stringifySearchWith } from '@tanstack/react-router'
import { MotionConfig } from 'motion/react'
import { routeTree } from './routeTree.gen'
import type { AskMode } from './types/repo/chat'

/** The routes are the files in pages/routes; the Vite plugin writes routeTree.gen.ts from them. */
const router = createRouter({
  routeTree,
  caseSensitive: true, // /Login is not /login
  // A new page starts at the top (a replace, or resetScroll: false, stays put); Back and Forward return where the reader was.
  scrollRestoration: true,
  // Query values stay the strings the address holds: the default reads them as JSON, and would write ?page="123".
  parseSearch: (search) => Object.fromEntries(new URLSearchParams(search)),
  stringifySearch: stringifySearchWith(String),
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
  /** What the repository page hands the search page in the history entry: a question to ask, and how. */
  interface HistoryState {
    question?: string
    mode?: AskMode
  }
}

export function App() {
  return (
    <MotionConfig reducedMotion="user">
      <RouterProvider router={router} />
    </MotionConfig>
  )
}
