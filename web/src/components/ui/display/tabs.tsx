import { motion } from 'motion/react'
import { Tabs as TabsPrimitive } from 'radix-ui'
import { createContext, useContext, useId, type ComponentProps } from 'react'
import { cn } from '../../../lib/style/cn'
import { slide } from '../../../lib/style/motion'

// The selected tab and this Tabs' own layoutId, so the underline slides between its triggers only.
const TabsContext = createContext({ value: undefined as string | undefined, layoutId: '' })

/** shadcn/ui's Tabs (line style), controlled: the selected tab's underline slides to the next one (motion layoutId). */
function Tabs({ className, value, ...props }: ComponentProps<typeof TabsPrimitive.Root>) {
  const layoutId = useId()
  return (
    <TabsContext value={{ value, layoutId }}>
      <TabsPrimitive.Root data-slot="tabs" value={value} className={cn('flex flex-col gap-3', className)} {...props} />
    </TabsContext>
  )
}

function TabsList({ className, ...props }: ComponentProps<typeof TabsPrimitive.List>) {
  return <TabsPrimitive.List data-slot="tabs-list" className={cn('flex gap-5 border-b text-muted-foreground', className)} {...props} />
}

function TabsTrigger({ className, value, children, ...props }: ComponentProps<typeof TabsPrimitive.Trigger>) {
  const tabs = useContext(TabsContext)
  return (
    <TabsPrimitive.Trigger
      data-slot="tabs-trigger"
      value={value}
      className={cn(
        'relative rounded-sm px-0.5 pt-1.5 pb-2.5 font-mono text-[11.5px] font-medium tracking-[0.14em] uppercase transition-colors outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50 data-[state=active]:text-foreground',
        className,
      )}
      {...props}
    >
      {children}
      {tabs.value === value && (
        <motion.span layoutId={tabs.layoutId} className="absolute inset-x-0 -bottom-px h-0.5 bg-brand" transition={slide} />
      )}
    </TabsPrimitive.Trigger>
  )
}

function TabsContent({ className, ...props }: ComponentProps<typeof TabsPrimitive.Content>) {
  return <TabsPrimitive.Content data-slot="tabs-content" className={cn('flex-1', className)} {...props} />
}

export { Tabs, TabsContent, TabsList, TabsTrigger }
