import { CodeXmlIcon } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import type { ReactNode } from 'react'
import { Card } from '../../components/ui/display/card'
import { hero, reveal } from '../../lib/style/motion'
import { Frame } from '../Frame'

/** The sign-in and sign-up pages' card, under its heading (id session-title, which names the form). */
export function SessionCard({ title, heading, children }: { title: string; heading: ReactNode; children: ReactNode }) {
  return (
    <Frame title={title}>
      <main className="px-4 pt-[12dvh] pb-20">
        <motion.div className="mx-auto w-full max-w-[380px]" variants={hero} initial="hidden" animate="shown">
          <motion.div variants={reveal}>
            <Card className="gap-6 bg-popover px-7 pt-8 pb-7 shadow-raised">
              <div className="grid justify-items-center gap-4">
                {/* The site's mark, as on its tab icon. */}
                <span
                  aria-hidden="true"
                  className="grid size-10 place-items-center rounded-lg bg-primary text-primary-foreground"
                >
                  <CodeXmlIcon className="size-5" />
                </span>
                <h1
                  id="session-title"
                  className="text-center font-serif text-[30px] leading-tight font-normal tracking-[-0.02em]"
                >
                  {heading}
                </h1>
              </div>
              {children}
            </Card>
          </motion.div>
        </motion.div>
      </main>
    </Frame>
  )
}

/** Why the last sign-in or sign-up failed, sliding in above the submit button. */
export function SessionError({ error }: { error: string }) {
  return (
    <AnimatePresence>
      {error && (
        <motion.p
          key={error}
          className="text-sm text-destructive"
          role="alert"
          initial={{ opacity: 0, y: -4 }}
          animate={{ opacity: 1, y: 0 }}
          exit={{ opacity: 0 }}
        >
          {error}
        </motion.p>
      )}
    </AnimatePresence>
  )
}
