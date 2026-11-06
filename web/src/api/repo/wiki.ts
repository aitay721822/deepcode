import type { Wiki } from '../../types/repo/wiki'
import { api } from '../client'

/** The wiki to show: the newest finished one, which can trail the published snapshot while the next is written. */
export const getWiki = (repoId: number) => api<Wiki>(`/api/repos/${repoId}/wiki`)
