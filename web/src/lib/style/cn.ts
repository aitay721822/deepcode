import { clsx, type ClassValue } from 'clsx'
import { extendTailwindMerge } from 'tailwind-merge'

// index.css's elevation scale (--shadow-*), so a shadow passed in replaces a component's own.
const twMerge = extendTailwindMerge({ extend: { theme: { shadow: ['raised', 'floating', 'modal'] } } })

/** Joins class names; a later Tailwind class wins over an earlier one it conflicts with (shadcn/ui's cn). */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
