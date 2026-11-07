import { type AnchorHTMLAttributes, useEffect, useState } from 'react'

const current = () => location.pathname + location.search

/** The current path + query; re-renders on navigation. */
export function useLocation() {
  const [path, setPath] = useState(current)
  useEffect(() => {
    const on = () => setPath(current())
    addEventListener('popstate', on)
    return () => removeEventListener('popstate', on)
  }, [])
  return path
}

/** Goes to `to`; a new page (not a replace) starts at the top, and the current URL is not pushed twice. */
export function navigate(to: string, opts: { replace?: boolean; state?: unknown } = {}) {
  if (opts.replace) history.replaceState(opts.state ?? null, '', to)
  else {
    if (to !== current() || opts.state !== undefined) history.pushState(opts.state ?? null, '', to)
    scrollTo(0, 0)
  }
  dispatchEvent(new PopStateEvent('popstate'))
}

export function Link({ to, onClick, ...props }: AnchorHTMLAttributes<HTMLAnchorElement> & { to: string }) {
  return (
    <a
      {...props}
      href={to}
      onClick={(e) => {
        onClick?.(e)
        if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
        e.preventDefault()
        navigate(to)
      }}
    />
  )
}
