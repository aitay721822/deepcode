import { AnimatePresence, motion } from 'motion/react'
import type { ReactNode } from 'react'
import { Card } from '../../components/ui/display/card'
import { hero, reveal } from '../../lib/style/motion'
import { Frame } from '../Frame'

/** The sign-in and sign-up pages' card, under its heading (id session-title, which names the form). */
export function SessionCard({ title, heading, children }: { title: string; heading: ReactNode; children: ReactNode }) {
  return (
    <Frame title={title}>
      <main className="relative isolate px-4 pt-[12dvh] pb-20">
        {/* Graph paper, fading out around the card. */}
        <div
          aria-hidden="true"
          className="dot-grid absolute inset-0 -z-10 [mask-image:radial-gradient(ellipse_at_50%_30%,black_10%,transparent_60%)]"
        />
        <motion.div className="mx-auto w-full max-w-[400px]" variants={hero} initial="hidden" animate="shown">
          <motion.div variants={reveal}>
            <Card className="gap-6 px-7 pt-8 pb-7 shadow-modal">
              <h1
                id="session-title"
                className="text-center font-serif text-[32px] leading-tight font-normal tracking-[-0.02em]"
              >
                {heading}
              </h1>
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
