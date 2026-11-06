import type { ReactNode } from 'react'
import { Link } from '../../lib/router'
import type { Repo } from '../../types/repo/repo'
import { ThemeToggle } from './ThemeToggle'

export function Header({ repo, children }: { repo?: Pick<Repo, 'id' | 'owner' | 'name'>; children?: ReactNode }) {
  return (
    <header className="sticky top-0 z-20 print:hidden flex h-16 items-center justify-between gap-3 border-b bg-background/85 px-4 backdrop-blur-md min-[900px]:px-6">
      {/* Registration crosses where the rule meets the frame's sides, once the frame stands clear of the window's edges. */}
      <span aria-hidden="true" className="reg-mark -bottom-[5px] -left-[5px] hidden min-[1580px]:block" />
      <span aria-hidden="true" className="reg-mark -right-[5px] -bottom-[5px] hidden min-[1580px]:block" />
      <div className="flex min-w-0 items-center gap-2.5">
        <Link to="/" className="group flex flex-none items-center gap-2 rounded-sm">
          <span aria-hidden="true" className="size-2.5 bg-brand transition-transform duration-300 group-hover:rotate-45" />
          <span className="whitespace-nowrap">
            <span className="font-serif text-[23px] leading-none font-medium tracking-[-0.02em] italic">deep</span>
            <span className="ml-0.5 font-mono text-[15px] leading-none font-medium">code</span>
          </span>
        </Link>
        {repo && (
          <>
            <span aria-hidden="true" className="text-lg font-light text-faint">/</span>
            <Link to={`/repos/${repo.id}`} className="truncate font-mono text-[13.5px] text-muted-foreground hover:text-foreground">{repo.owner}/{repo.name}</Link>
          </>
        )}
      </div>
      <div className="flex flex-none items-center gap-1.5">
        <ThemeToggle />
        {children}
      </div>
    </header>
  )
}
