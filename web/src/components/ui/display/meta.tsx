import type { ReactNode } from 'react'

/** A term and its value in a details list (a dl laid out as a row of them). */
function Meta({ term, children }: { term: string; children: ReactNode }) {
  return (
    <div>
      <dt className="eyebrow text-muted-foreground">{term}</dt>
      <dd className="mt-1">{children}</dd>
    </div>
  )
}

export { Meta }
