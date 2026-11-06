import { ArrowUpIcon } from 'lucide-react'
import { useId, useState } from 'react'
import { Button } from '../ui/controls/button'
import { Spinner } from '../ui/feedback/spinner'
import { Textarea } from '../ui/form/textarea'

/** The question box docked at the bottom of the window. */
export function AskBox({ placeholder, disabled, busy, onAsk }: { placeholder: string; disabled?: boolean; busy?: boolean; onAsk: (q: string) => void }) {
  const [text, setText] = useState('')
  const hint = useId()
  const submit = () => {
    const q = text.trim()
    if (!q || disabled || busy) return
    onAsk(q)
    setText('')
  }
  return (
    <div className="pointer-events-none fixed inset-x-0 bottom-4 z-30 print:hidden flex justify-center px-4">
      <form className="pointer-events-auto w-full max-w-[760px] rounded-xl border border-brand/40 bg-popover/90 shadow-floating ring-4 ring-brand/8 backdrop-blur-xl transition-[border-color,box-shadow] focus-within:border-brand/80 focus-within:ring-brand/15"
        onSubmit={(e) => {
          e.preventDefault()
          submit()
        }}>
        <Textarea value={text} rows={2} placeholder={placeholder} aria-label={placeholder} aria-describedby={hint} disabled={disabled}
          className="max-h-48 min-h-0 resize-none rounded-none border-0 bg-transparent px-4 pt-3.5 pb-1 focus-visible:ring-0 md:text-[15px]"
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            // Enter sends; Shift+Enter breaks the line; IME composition (e.g. 注音) is left alone. Safari sends the Enter
            // that picks a candidate after the composition ends, so isComposing is false; its keyCode is still 229.
            if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing && e.keyCode !== 229) {
              e.preventDefault()
              submit()
            }
          }} />
        <div className="flex items-center gap-3 px-3 pt-1 pb-2.5">
          <span id={hint} className="eyebrow pl-1 text-muted-foreground max-[520px]:hidden">Enter to send · Shift+Enter for a new line</span>
          <Button type="submit" size="icon-sm" className="ml-auto rounded-full bg-brand text-brand-foreground hover:bg-brand/90" aria-label="Send"
            disabled={disabled || busy || !text.trim()}>
            {busy ? <Spinner /> : <ArrowUpIcon />}
          </Button>
        </div>
      </form>
    </div>
  )
}
