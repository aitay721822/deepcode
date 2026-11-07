import { AnimatePresence, motion } from 'motion/react'
import { type ReactNode, useEffect } from 'react'
import { Link, navigate } from '../../lib/router'
import { cn } from '../../lib/style/cn'
import { markerRadius, rise, slide } from '../../lib/style/motion'
import { isId } from '../../lib/url'
import type { Permission } from '../../types/auth'
import { Frame } from '../Frame'
import { NotFoundPage } from '../NotFoundPage'
import { useSession } from '../session/session'
import { JobDetail } from './jobs/JobDetail'
import { JobsSection } from './jobs/JobsSection'
import { RolesSection } from './roles/RolesSection'
import { ServersSection } from './servers/ServersSection'
import { UsersSection } from './users/UsersSection'

const sections: { path: string; label: string; permission: Permission; page: (id?: number) => ReactNode }[] = [
  {
    path: 'jobs',
    label: 'Jobs',
    permission: 'jobs.view',
    page: (id) => (id ? <JobDetail id={id} /> : <JobsSection />),
  },
  { path: 'users', label: 'Users', permission: 'users.manage', page: () => <UsersSection /> },
  { path: 'roles', label: 'Roles', permission: 'users.manage', page: () => <RolesSection /> },
  { path: 'servers', label: 'Git servers', permission: 'servers.manage', page: () => <ServersSection /> },
]

/** /admin[/:section[/:id]]: the sections the reader's permissions allow, beside their nav; /admin opens the first. */
export function AdminPage({ section, id }: { section?: string; id?: string }) {
  const { can } = useSession()
  const allowed = sections.filter((s) => can(s.permission))
  const first = allowed[0]?.path
  useEffect(() => {
    if (!section && first) navigate(`/admin/${first}`, { replace: true })
  }, [section, first])

  if (!allowed.length) return <NotFoundPage message="You do not have access to the admin console." />
  if (!section) return null // on the way to the first section
  const current = sections.find((s) => s.path === section)
  if (!current || (id !== undefined && (section !== 'jobs' || !isId(id)))) return <NotFoundPage />
  if (!allowed.includes(current)) return <NotFoundPage message="You do not have access to this section." />

  return (
    <Frame title="Admin">
      <div className="grid grid-cols-1 min-[900px]:grid-cols-[200px_minmax(0,1fr)]">
        {/* A column beside the section; on narrow screens a row above it. The current section's pill slides (layoutId). */}
        <nav
          aria-label="Admin console"
          className="isolate flex gap-0.5 overflow-x-auto border-b px-4 py-3 min-[900px]:sticky min-[900px]:top-16 min-[900px]:flex-col min-[900px]:self-start min-[900px]:border-b-0 min-[900px]:py-8"
        >
          {allowed.map((s, i) => (
            <Link
              key={s.path}
              to={`/admin/${s.path}`}
              aria-current={s === current ? 'page' : undefined}
              className={cn(
                'relative flex items-baseline gap-2.5 rounded-sm px-3 py-2 font-mono text-[11.5px] font-medium tracking-[0.12em] whitespace-nowrap text-muted-foreground uppercase transition-colors hover:text-foreground',
                s === current && 'font-semibold text-foreground',
              )}
            >
              {s === current && (
                <motion.span
                  layoutId="admin-nav"
                  className="absolute inset-0 -z-10 bg-accent forced-colors:border"
                  transition={slide}
                  style={{ borderRadius: markerRadius }}
                />
              )}
              <span aria-hidden="true" className={cn('text-[10px]', s === current ? 'text-brand' : 'text-faint')}>
                {String(i + 1).padStart(2, '0')}
              </span>
              {s.label}
            </Link>
          ))}
        </nav>
        <main
          tabIndex={-1}
          className="min-w-0 px-4 pt-5 pb-16 outline-none min-[900px]:border-l min-[900px]:px-10 min-[900px]:pt-7 min-[900px]:pb-20"
        >
          <AnimatePresence mode="wait" initial={false}>
            <motion.div key={`${section}/${id ?? ''}`} {...rise}>
              {current.page(id ? +id : undefined)}
            </motion.div>
          </AnimatePresence>
        </main>
      </div>
    </Frame>
  )
}
