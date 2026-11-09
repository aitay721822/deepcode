import { CheckIcon, CopyIcon, XIcon } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Button } from '../../../components/ui/controls/button'
import { cn } from '../../../lib/style/cn'

/** A value to copy, such as a URL, a command or a token's secret: shown in full, with a copy button beside it. */
export function CopyField({ value, label, className }: { value: string; label: string; className?: string }) {
  // A new object each click, so a second click starts the tick's time anew.
  const [copied, setCopied] = useState<{ text: '已複製' | '複製失敗' } | null>(null)
  const copy = copied?.text ?? ''
  useEffect(() => {
    if (!copied) return
    const t = setTimeout(() => setCopied(null), 1500) // a transient tick, or cross
    return () => clearTimeout(t)
  }, [copied])
  return (
    <div className={cn('flex items-start gap-2 rounded-lg border bg-code py-1.5 pr-1.5 pl-3', className)}>
      <code className="min-w-0 flex-1 py-1 font-mono text-[12.5px] leading-relaxed [overflow-wrap:anywhere] whitespace-pre-wrap">
        {value}
      </code>
      <Button
        variant="ghost"
        size="icon-sm"
        className="shrink-0 text-muted-foreground"
        aria-label={`複製${label}`}
        onClick={async () => {
          try {
            await navigator.clipboard.writeText(value) // no clipboard outside https or localhost, or permission denied
            setCopied({ text: '已複製' })
          } catch {
            setCopied({ text: '複製失敗' })
          }
        }}
      >
        {copy === '已複製' ? <CheckIcon /> : copy ? <XIcon /> : <CopyIcon />}
      </Button>
      <span className="sr-only" role="status">
        {copy}
      </span>
    </div>
  )
}
