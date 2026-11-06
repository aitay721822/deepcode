import { Loader2Icon } from 'lucide-react'
import type { ComponentProps } from 'react'
import { cn } from '../../../lib/style/cn'

/** Decorative (lucide hides it from screen readers) beside text that says what is happening; give it an aria-label to stand alone. */
function Spinner({ className, ...props }: ComponentProps<'svg'>) {
  return <Loader2Icon className={cn('size-4 animate-spin', className)} {...props} />
}

export { Spinner }
