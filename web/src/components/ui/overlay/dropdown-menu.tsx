import { AnimatePresence, motion } from 'motion/react'
import { DropdownMenu as DropdownMenuPrimitive } from 'radix-ui'
import { createContext, useContext, type ComponentProps } from 'react'
import { cn } from '../../../lib/style/cn'
import { pop } from '../../../lib/style/motion'

// As in dialog.tsx: the content stays mounted (forceMount) until motion has played its exit.
const OpenContext = createContext(false)

/** shadcn/ui's DropdownMenu, controlled (open + onOpenChange) so that its content animates in and out. */
function DropdownMenu({ open = false, ...props }: ComponentProps<typeof DropdownMenuPrimitive.Root>) {
  return (
    <OpenContext value={open}>
      <DropdownMenuPrimitive.Root data-slot="dropdown-menu" open={open} {...props} />
    </OpenContext>
  )
}

function DropdownMenuTrigger(props: ComponentProps<typeof DropdownMenuPrimitive.Trigger>) {
  return <DropdownMenuPrimitive.Trigger data-slot="dropdown-menu-trigger" {...props} />
}

function DropdownMenuContent({ className, children, sideOffset = 4, ...props }: ComponentProps<typeof DropdownMenuPrimitive.Content>) {
  const open = useContext(OpenContext)
  return (
    <AnimatePresence>
      {open && (
        <DropdownMenuPrimitive.Portal forceMount>
          <DropdownMenuPrimitive.Content forceMount asChild sideOffset={sideOffset} {...props}>
            <motion.div
              data-slot="dropdown-menu-content"
              className={cn(
                'z-50 max-h-(--radix-dropdown-menu-content-available-height) min-w-[8rem] origin-(--radix-dropdown-menu-content-transform-origin) overflow-x-hidden overflow-y-auto rounded-lg border bg-popover p-1 text-popover-foreground shadow-floating',
                className,
              )}
              {...pop}
            >
              {children}
            </motion.div>
          </DropdownMenuPrimitive.Content>
        </DropdownMenuPrimitive.Portal>
      )}
    </AnimatePresence>
  )
}

function DropdownMenuItem({ className, ...props }: ComponentProps<typeof DropdownMenuPrimitive.Item>) {
  return (
    <DropdownMenuPrimitive.Item
      data-slot="dropdown-menu-item"
      className={cn(
        "relative flex cursor-default items-center gap-2 rounded-sm px-2 py-1.5 text-sm select-none focus:outline-hidden focus:bg-accent focus:text-accent-foreground data-[disabled]:pointer-events-none data-[disabled]:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 [&_svg:not([class*='text-'])]:text-muted-foreground",
        className,
      )}
      {...props}
    />
  )
}

function DropdownMenuSeparator({ className, ...props }: ComponentProps<typeof DropdownMenuPrimitive.Separator>) {
  return <DropdownMenuPrimitive.Separator data-slot="dropdown-menu-separator" className={cn('-mx-1 my-1 h-px bg-border', className)} {...props} />
}

export { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger }
