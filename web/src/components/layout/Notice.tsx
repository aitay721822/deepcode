import type { ReactNode } from 'react'

/** A short centred message in place of a page's content: loading, missing, not allowed, failed. */
export function Notice({ title, children }: { title?: string; children?: ReactNode }) {
  return (
    <div className="mx-auto my-20 max-w-[560px] space-y-4 px-6 text-center">
      {title && <h1 className="font-serif text-[30px] leading-tight font-medium tracking-[-0.015em]">{title}</h1>}
      {children}
    </div>
  )
}
