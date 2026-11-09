import { ArrowUpIcon } from 'lucide-react'
import { useId, useState } from 'react'
import type { AskMode } from '../../types/repo/chat'
import { Button } from '../ui/controls/button'
import { Spinner } from '../ui/feedback/spinner'
import { Textarea } from '../ui/form/textarea'

const modes: { value: AskMode; label: string; hint: string }[] = [
  { value: 'fast', label: 'Fast', hint: '搜尋一輪後回答' },
  { value: 'deep', label: 'Deep', hint: '最多搜尋三輪，並先讀最相關的 Wiki 頁面：回答更完整，但慢兩到三倍' },
  { value: 'codemap', label: 'Codemap', hint: '畫出回答問題的程式路徑：依執行順序編號的程式碼位置，也能切換成圖' },
]

/** The question box docked at the bottom of the window, with how to answer: fast, deep (slower), or as a code map. */
export function AskBox({
  placeholder,
  disabled,
  busy,
  defaultMode = 'fast',
  onAsk,
}: {
  placeholder: string
  disabled?: boolean
  busy?: boolean
  defaultMode?: AskMode
  onAsk: (q: string, mode: AskMode) => void
}) {
  const [text, setText] = useState('')
  const [mode, setMode] = useState(defaultMode)
  const hint = useId()
  const group = useId()
  const submit = () => {
    const q = text.trim()
    if (!q || disabled || busy) return
    onAsk(q, mode)
    setText('')
  }
  return (
    <div className="pointer-events-none fixed inset-x-0 bottom-4 z-30 print:hidden flex justify-center px-4">
      <form
        className="pointer-events-auto w-full max-w-[760px] rounded-xl border border-brand/40 bg-popover/90 shadow-floating ring-4 ring-brand/8 backdrop-blur-xl transition-[border-color,box-shadow] focus-within:border-brand/80 focus-within:ring-brand/15"
        onSubmit={(e) => {
          e.preventDefault()
          submit()
        }}
      >
        <Textarea
          value={text}
          rows={2}
          placeholder={placeholder}
          aria-label={placeholder}
          aria-describedby={hint}
          disabled={disabled}
          className="max-h-48 min-h-0 resize-none rounded-none border-0 bg-transparent px-4 pt-3.5 pb-1 focus-visible:ring-0 md:text-[15px]"
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            // Enter sends; Shift+Enter breaks the line; IME composition (e.g. 注音) is left alone. Safari sends the Enter
            // that picks a candidate after the composition ends, so isComposing is false; its keyCode is still 229.
            if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing && e.keyCode !== 229) {
              e.preventDefault()
              submit()
            }
          }}
        />
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 pt-1 pb-2.5">
          <span id={hint} className="sr-only">
            Enter 送出，Shift+Enter 換行
          </span>
          {/* What the chosen mode does, for those who cannot hover its title (a line of its own on a phone); screen
              readers hear it with the radio. */}
          <span
            aria-hidden="true"
            className="order-last min-w-0 basis-full pl-1 text-[12px] text-muted-foreground sm:order-none sm:flex-1 sm:basis-0 sm:truncate"
          >
            {modes.find((m) => m.value === mode)?.hint}
          </span>
          {/* Native radios: arrow keys move between the modes; the chosen one sits on a lighter ground. */}
          <div
            role="radiogroup"
            aria-label="回答方式"
            className="ml-auto flex shrink-0 rounded-lg border bg-muted p-0.5"
          >
            {modes.map((m) => (
              <label
                key={m.value}
                title={m.hint}
                className="cursor-pointer rounded-md px-2.5 py-1 font-mono text-[12px] text-muted-foreground transition-colors select-none hover:text-foreground has-checked:bg-background has-checked:text-foreground has-checked:shadow-xs has-disabled:cursor-not-allowed has-disabled:opacity-50 has-focus-visible:ring-2 has-focus-visible:ring-ring"
              >
                <input
                  type="radio"
                  name={group}
                  value={m.value}
                  checked={mode === m.value}
                  disabled={disabled}
                  className="sr-only"
                  onChange={() => setMode(m.value)}
                />
                {m.label}
                <span className="sr-only">：{m.hint}</span>
              </label>
            ))}
          </div>
          <Button
            type="submit"
            size="icon-sm"
            className="rounded-full bg-brand text-brand-foreground hover:bg-brand/90"
            aria-label="送出"
            disabled={disabled || busy || !text.trim()}
          >
            {busy ? <Spinner /> : <ArrowUpIcon />}
          </Button>
        </div>
      </form>
    </div>
  )
}
