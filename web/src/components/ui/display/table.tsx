import { type HTMLMotionProps, motion } from 'motion/react'
import type { ComponentProps } from 'react'
import { cn } from '../../../lib/style/cn'
import { item, list } from '../../../lib/style/motion'

/** shadcn/ui's Table, in a bordered box that scrolls sideways itself, never the page. */
function Table({ className, ...props }: ComponentProps<'table'>) {
  return (
    <div data-slot="table-container" className="relative w-full overflow-x-auto rounded-lg border bg-card">
      <table data-slot="table" className={cn('w-full caption-bottom text-sm', className)} {...props} />
    </div>
  )
}

function TableHeader({ className, ...props }: ComponentProps<'thead'>) {
  return <thead data-slot="table-header" className={cn('[&_tr]:border-b', className)} {...props} />
}

/** Its rows rise in one after another (motion), and so do rows added later. */
function TableBody({ className, ...props }: HTMLMotionProps<'tbody'>) {
  return (
    <motion.tbody
      data-slot="table-body"
      className={cn('[&_tr:last-child]:border-0', className)}
      variants={list}
      initial="hidden"
      animate="shown"
      {...props}
    />
  )
}

function TableRow({ className, ...props }: HTMLMotionProps<'tr'>) {
  return (
    <motion.tr
      data-slot="table-row"
      className={cn('border-b transition-colors hover:bg-accent/45 data-[state=selected]:bg-muted', className)}
      variants={item}
      {...props}
    />
  )
}

function TableHead({ className, ...props }: ComponentProps<'th'>) {
  return (
    <th
      data-slot="table-head"
      className={cn(
        'h-10 px-3 text-left align-middle font-mono text-[10.5px] font-medium tracking-[0.14em] whitespace-nowrap text-muted-foreground uppercase',
        className,
      )}
      {...props}
    />
  )
}

function TableCell({ className, ...props }: ComponentProps<'td'>) {
  return (
    <td
      data-slot="table-cell"
      className={cn('px-3 py-2.5 align-middle whitespace-nowrap tabular-nums', className)}
      {...props}
    />
  )
}

export { Table, TableBody, TableCell, TableHead, TableHeader, TableRow }
