import type { Transition, Variants } from 'motion/react'

const ease = [0.22, 1, 0.36, 1] as const // a quick start and a long, soft settle
const quick: Transition = { duration: 0.26, ease }

/** Fades in with a small rise and fades out in place: pages, sections, articles. */
export const rise = {
  initial: { opacity: 0, y: 8 },
  animate: { opacity: 1, y: 0, transition: quick },
  exit: { opacity: 0, transition: { duration: 0.12 } },
}

/** Pops a floating surface (menu, dialog) in and out. */
export const pop = {
  initial: { opacity: 0, scale: 0.96, y: -4 },
  animate: { opacity: 1, scale: 1, y: 0, transition: { duration: 0.18, ease } },
  exit: { opacity: 0, scale: 0.96, y: -4, transition: { duration: 0.12 } },
} as const

/** Fades a backdrop in and out. */
export const fade = {
  initial: { opacity: 0 },
  animate: { opacity: 1, transition: { duration: 0.15 } },
  exit: { opacity: 0, transition: { duration: 0.12 } },
}

/** A current-item marker gliding to the next item (motion layoutId): nav highlights, tab underlines. */
export const slide: Transition = { duration: 0.3, ease }

/** A sliding marker's corner radius in px (--radius-sm, rounded-sm): set through style, as motion keeps it true mid-slide only then. */
export const markerRadius = 6

/** A card that rises under the pointer and gives a little when pressed. */
export const lift = { whileHover: { y: -3 }, whileTap: { scale: 0.99 } }

/** A `list` parent rises its `item` children in one after another: initial="hidden" animate="shown". */
export const list: Variants = {
  hidden: {},
  // Past a dozen children the rest come in together, so long lists do not crawl.
  shown: { transition: { delayChildren: (i: number) => Math.min(i, 12) * 0.035 } },
}

export const item: Variants = {
  hidden: { opacity: 0, y: 10 },
  shown: { opacity: 1, y: 0, transition: { duration: 0.4, ease } },
}

/** A page's opening lines come in one by one, slower than a list: a `hero` parent and `reveal` children. */
export const hero: Variants = {
  hidden: {},
  shown: { transition: { delayChildren: (i: number) => i * 0.09 } },
}

export const reveal: Variants = {
  hidden: { opacity: 0, y: 14 },
  shown: { opacity: 1, y: 0, transition: { duration: 0.6, ease } },
}
