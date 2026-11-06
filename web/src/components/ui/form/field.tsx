import { cloneElement, useId, type ReactElement, type ReactNode } from 'react'
import { Label } from './label'

type Control = { id: string; 'aria-describedby'?: string }

/**
 * A label over its control and an optional hint under it, linked to the control as its description.
 * The child is the control (it gets the id), or a function that places the id itself, e.g. on a SelectTrigger.
 */
function Field({ label, hint, children }: { label: ReactNode; hint?: ReactNode; children: ReactElement<Partial<Control>> | ((c: Control) => ReactNode) }) {
  const id = useId()
  const control = { id, 'aria-describedby': hint ? `${id}-hint` : undefined }
  return (
    <div data-slot="field" className="grid gap-2">
      <Label htmlFor={id}>{label}</Label>
      {typeof children === 'function' ? children(control) : cloneElement(children, control)}
      {hint && <p id={`${id}-hint`} className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  )
}

export { Field }
