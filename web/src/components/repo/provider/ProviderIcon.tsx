import type { SVGProps } from 'react'
import { cn } from '../../../lib/style/cn'
import type { Provider } from '../../../types/repo/repo'

// lucide-react has no brand marks; these are drawn its way (24px grid, 2px round strokes).
const paths: Record<Provider, string> = {
  github:
    'M9 19c-5 1.5-5-2.5-7-3m14 6v-3.9a3.4 3.4 0 0 0-.9-2.6c3.1-.4 6.4-1.5 6.4-7a5.4 5.4 0 0 0-1.5-3.8 5.1 5.1 0 0 0-.1-3.8s-1.2-.3-3.9 1.5a13.4 13.4 0 0 0-7 0C6.3.7 5.1 1 5.1 1a5.1 5.1 0 0 0-.1 3.8 5.4 5.4 0 0 0-1.5 3.8c0 5.4 3.3 6.6 6.4 7a3.4 3.4 0 0 0-.9 2.6V22',
  gitlab: 'M12 21.5L2.2 14.4l2.4-11 2.9 7.3h9l2.9-7.3 2.4 11z',
}

/** GitHub's or GitLab's mark, sized and stroked like a lucide icon. */
export function ProviderIcon({ provider, className, ...props }: { provider: Provider } & SVGProps<SVGSVGElement>) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      className={cn('size-4 shrink-0', className)}
      {...props}
    >
      <path d={paths[provider]} />
    </svg>
  )
}
