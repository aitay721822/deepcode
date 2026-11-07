import { Slot } from 'radix-ui'
import type { ComponentProps } from 'react'
import { cn } from '../../../lib/style/cn'

/** shadcn/ui's Card; asChild draws it on its child, e.g. a link or a motion element (pass overrides here, not on the child). */
function Card({ className, asChild, ...props }: ComponentProps<'div'> & { asChild?: boolean }) {
  const Comp = asChild ? Slot.Root : 'div'
  return (
    <Comp
      data-slot="card"
      className={cn('flex flex-col gap-6 rounded-xl border bg-card py-6 text-card-foreground', className)}
      {...props}
    />
  )
}

export { Card }
