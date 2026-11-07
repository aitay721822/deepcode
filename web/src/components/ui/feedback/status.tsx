import { type ComponentProps, useEffect, useState } from 'react'

/**
 * A role=status line for a message that appears with its text: a live region speaks what changes once
 * it is there, not what it arrives with, so this one mounts empty and takes its text a moment later.
 */
function Status({ children, ...props }: ComponentProps<'p'>) {
  const [ready, setReady] = useState(false)
  useEffect(() => setReady(true), [])
  return (
    <p role="status" {...props}>
      {ready && children}
    </p>
  )
}

export { Status }
