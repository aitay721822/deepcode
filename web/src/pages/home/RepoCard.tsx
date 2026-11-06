import { ArrowRightIcon, StarIcon } from 'lucide-react'
import { motion } from 'motion/react'
import { ProviderBadge } from '../../components/repo/provider/ProviderBadge'
import { isActive, stageLabel } from '../../components/repo/jobStage'
import { Card } from '../../components/ui/display/card'
import { formatStars } from '../../lib/format'
import { Link } from '../../lib/router'
import { item, lift } from '../../lib/style/motion'
import type { Repo } from '../../types/repo/repo'

const MotionLink = motion.create(Link)

/** A repository as a catalogue card: where it comes from, its name, what it is, and how it stands. */
export function RepoCard({ repo }: { repo: Repo }) {
  const failed = repo.job?.status === 'failed' && !repo.snapshot
  return (
    <Card asChild className="group min-h-40 items-start gap-0 px-4 pt-3.5 pb-3 transition-colors hover:border-brand/55">
      <MotionLink to={`/repos/${repo.id}`} variants={item} {...lift}>
        <span className="flex w-full items-center justify-between gap-3">
          <ProviderBadge repo={repo} />
          <span aria-hidden="true" className="flex-none font-mono text-[10.5px] tracking-[0.12em] text-faint">#{String(repo.id).padStart(3, '0')}</span>
        </span>
        <span className="mt-3 font-serif text-[20px] leading-[1.2] tracking-[-0.01em] [overflow-wrap:anywhere]">
          <span className="text-muted-foreground">{repo.owner}/</span>{repo.name}
        </span>
        {repo.description && <span className="mt-2 line-clamp-2 text-[13px] leading-normal text-muted-foreground">{repo.description}</span>}
        <span className="mt-auto w-full pt-4">
          <span className="flex items-center gap-3 border-t border-dashed pt-2.5 font-mono text-[11.5px] text-muted-foreground">
            <span className="inline-flex items-center gap-1"><StarIcon className="size-3 fill-current" /> {formatStars(repo.stars)}</span>
            {isActive(repo.job) && (
              <span className="min-w-0 truncate">{stageLabel(repo.job!)}</span>
            )}
            {failed && <span className="text-destructive">Indexing failed</span>}
            <ArrowRightIcon className="ml-auto size-4 flex-none text-foreground transition-transform duration-300 group-hover:translate-x-1" />
          </span>
        </span>
      </MotionLink>
    </Card>
  )
}
