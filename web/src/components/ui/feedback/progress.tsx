import { motion, useReducedMotion } from 'motion/react'
import { Progress as ProgressPrimitive } from 'radix-ui'
import type { ComponentProps } from 'react'
import { cn } from '../../../lib/style/cn'

// [bar, track]
const tones = {
  primary: ['bg-primary', 'bg-primary/15'],
  warning: ['bg-warning', 'bg-warning/20'], // work in progress
  muted: ['bg-muted-foreground/50', 'bg-muted-foreground/15'], // work that has stopped
}

/**
 * shadcn/ui's Progress; the bar grows to value (motion animates its width). Without a value it is
 * indeterminate and a short bar sweeps across.
 */
function Progress({ className, value, tone = 'primary', ...props }: ComponentProps<typeof ProgressPrimitive.Root> & { tone?: keyof typeof tones }) {
  const [bar, track] = tones[tone]
  const still = useReducedMotion() // no sweep, and the bar jumps to its value
  return (
    <ProgressPrimitive.Root data-slot="progress" value={value} className={cn('relative h-1 w-full overflow-hidden rounded-full', track, className)} {...props}>
      <ProgressPrimitive.Indicator data-slot="progress-indicator" asChild>
        {value == null ? (
          <motion.div className={cn('h-full w-1/3 rounded-full', bar)} animate={still ? undefined : { x: ['-100%', '300%'] }} transition={{ duration: 1.6, ease: 'easeInOut', repeat: Infinity }} />
        ) : (
          <motion.div className={cn('h-full rounded-full', bar)} initial={{ width: 0 }} animate={{ width: `${value}%` }} transition={still ? { duration: 0 } : { duration: 0.4, ease: 'easeOut' }} />
        )}
      </ProgressPrimitive.Indicator>
    </ProgressPrimitive.Root>
  )
}

export { Progress }
