import { XIcon } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { Dialog as DialogPrimitive } from 'radix-ui'
import { type ComponentProps, createContext, useContext } from 'react'
import { cn } from '../../../lib/style/cn'
import { fade, pop } from '../../../lib/style/motion'

// Whether the dialog is open, for DialogContent: Radix keeps the content mounted (forceMount)
// and AnimatePresence unmounts it once motion has played its exit.
const OpenContext = createContext(false)

/** shadcn/ui's Dialog, controlled (open + onOpenChange) so that its content animates in and out. */
function Dialog({ open = false, ...props }: ComponentProps<typeof DialogPrimitive.Root>) {
  return (
    <OpenContext value={open}>
      <DialogPrimitive.Root data-slot="dialog" open={open} {...props} />
    </OpenContext>
  )
}

function DialogContent({ className, children, ...props }: ComponentProps<typeof DialogPrimitive.Content>) {
  const open = useContext(OpenContext)
  return (
    <AnimatePresence>
      {open && (
        <DialogPrimitive.Portal forceMount>
          <DialogPrimitive.Overlay forceMount asChild>
            <motion.div
              data-slot="dialog-overlay"
              className="fixed inset-0 z-50 bg-overlay backdrop-blur-[2px]"
              {...fade}
            />
          </DialogPrimitive.Overlay>
          <DialogPrimitive.Content forceMount asChild {...props}>
            <motion.div
              data-slot="dialog-content"
              className={cn(
                'fixed top-1/2 left-1/2 z-50 flex max-h-[calc(100dvh-2rem)] w-full max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-xl border bg-popover text-popover-foreground shadow-modal outline-none sm:max-w-lg',
                className,
              )}
              {...pop}
            >
              {/* Only the body scrolls, so the close button stays in view in a dialog taller than the screen;
                  its padded background hides the text scrolling under it, and it sits clear of the body's scrollbar.
                  A long unbroken name (a title such as "Edit <username>") wraps instead of widening the body. */}
              <div className="grid min-h-0 gap-4 overflow-y-auto p-6 [overflow-wrap:anywhere]">{children}</div>
              <DialogPrimitive.Close className="absolute top-4 right-4 rounded-md bg-popover p-1 text-muted-foreground transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background focus:outline-hidden disabled:pointer-events-none [&_svg]:pointer-events-none [&_svg]:size-4">
                <XIcon />
                <span className="sr-only">Close</span>
              </DialogPrimitive.Close>
            </motion.div>
          </DialogPrimitive.Content>
        </DialogPrimitive.Portal>
      )}
    </AnimatePresence>
  )
}

function DialogHeader({ className, ...props }: ComponentProps<'div'>) {
  return (
    <div
      data-slot="dialog-header"
      className={cn('flex flex-col gap-2 text-center sm:text-left', className)}
      {...props}
    />
  )
}

function DialogFooter({ className, ...props }: ComponentProps<'div'>) {
  return (
    <div
      data-slot="dialog-footer"
      className={cn('flex flex-col-reverse gap-2 sm:flex-row sm:justify-end', className)}
      {...props}
    />
  )
}

function DialogTitle({ className, ...props }: ComponentProps<typeof DialogPrimitive.Title>) {
  return (
    <DialogPrimitive.Title
      data-slot="dialog-title"
      className={cn('pr-6 font-serif text-[23px] leading-tight font-medium tracking-[-0.01em]', className)}
      {...props}
    />
  )
}

export { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle }
