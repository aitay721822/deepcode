import { FileIcon } from 'lucide-react'
import { motion } from 'motion/react'
import type { Citation } from '../../types/repo/code'

export function CitationChip({ path, startLine, endLine, onClick }: Citation & { onClick?: () => void }) {
  return (
    <motion.button
      type="button"
      title={`${path}:${startLine}-${endLine}`}
      onClick={onClick}
      whileTap={{ scale: 0.95 }}
      className="mx-0.5 inline-flex h-[22px] max-w-full items-center gap-1.5 overflow-hidden rounded-sm border border-brand/35 bg-chip pl-[7px] align-middle font-mono text-[11px] leading-none font-medium whitespace-nowrap text-foreground transition-colors hover:border-brand"
    >
      <FileIcon className="size-3 shrink-0 text-brand" />
      <span className="min-w-0 truncate">{path.split('/').pop()}</span>
      <span className="inline-flex h-full shrink-0 items-center border-l border-brand/35 px-[7px] text-brand">
        {startLine === endLine ? startLine : `${startLine}-${endLine}`}
      </span>
    </motion.button>
  )
}
