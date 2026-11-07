import { AnimatePresence, motion } from 'motion/react'
import { AlertDialog as AlertDialogPrimitive } from 'radix-ui'
import { type ComponentProps, createContext, useContext } from 'react'
import { cn } from '../../../lib/style/cn'
import { fade, pop } from '../../../lib/style/motion'
import { Button } from '../controls/button'

// As in dialog.tsx: the content stays mounted (forceMount) until motion has played its exit.
const OpenContext = createContext(false)

/** shadcn/ui's AlertDialog, controlled (open + onOpenChange) so that its content animates in and out. */
function AlertDialog({ open = false, ...props }: ComponentProps<typeof AlertDialogPrimitive.Root>) {
  return (
    <OpenContext value={open}>
      <AlertDialogPrimitive.Root data-slot="alert-dialog" open={open} {...props} />
    </OpenContext>
  )
}

function AlertDialogContent({ className, children, ...props }: ComponentProps<typeof AlertDialogPrimitive.Content>) {
  const open = useContext(OpenContext)
  return (
    <AnimatePresence>
      {open && (
        <AlertDialogPrimitive.Portal forceMount>
          <AlertDialogPrimitive.Overlay forceMount asChild>
            <motion.div
              data-slot="alert-dialog-overlay"
              className="fixed inset-0 z-50 bg-overlay backdrop-blur-[2px]"
              {...fade}
            />
          </AlertDialogPrimitive.Overlay>
          <AlertDialogPrimitive.Content forceMount asChild {...props}>
            <motion.div
              data-slot="alert-dialog-content"
              className={cn(
                // overflow-wrap: a long unbroken name in the title ("Delete <name>?") wraps instead of pushing the buttons off a phone's screen
                'fixed top-1/2 left-1/2 z-50 grid max-h-[calc(100dvh-2rem)] w-full max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 gap-4 overflow-y-auto rounded-xl border bg-popover p-6 text-popover-foreground shadow-modal outline-none [overflow-wrap:anywhere] sm:max-w-lg',
                className,
              )}
              {...pop}
            >
              {children}
            </motion.div>
          </AlertDialogPrimitive.Content>
        </AlertDialogPrimitive.Portal>
      )}
    </AnimatePresence>
  )
}

function AlertDialogHeader({ className, ...props }: ComponentProps<'div'>) {
  return (
    <div
      data-slot="alert-dialog-header"
      className={cn('flex flex-col gap-2 text-center sm:text-left', className)}
      {...props}
    />
  )
}

function AlertDialogFooter({ className, ...props }: ComponentProps<'div'>) {
  return (
    <div
      data-slot="alert-dialog-footer"
      className={cn('flex flex-col-reverse gap-2 sm:flex-row sm:justify-end', className)}
      {...props}
    />
  )
}

function AlertDialogTitle({ className, ...props }: ComponentProps<typeof AlertDialogPrimitive.Title>) {
  return (
    <AlertDialogPrimitive.Title
      data-slot="alert-dialog-title"
      className={cn('font-serif text-[23px] leading-tight font-medium tracking-[-0.01em]', className)}
      {...props}
    />
  )
}

function AlertDialogDescription({ className, ...props }: ComponentProps<typeof AlertDialogPrimitive.Description>) {
  return (
    <AlertDialogPrimitive.Description
      data-slot="alert-dialog-description"
      className={cn('text-sm text-muted-foreground', className)}
      {...props}
    />
  )
}

/** Closes the dialog; Radix focuses it when the dialog opens. */
function AlertDialogCancel({ variant = 'outline', ...props }: ComponentProps<typeof Button>) {
  return (
    <AlertDialogPrimitive.Cancel asChild>
      <Button data-slot="alert-dialog-cancel" variant={variant} {...props} />
    </AlertDialogPrimitive.Cancel>
  )
}

export {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
}
