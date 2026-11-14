import type { UsageRow } from '../../types/admin/usage'
import { api } from '../client'

/** Each repository's spend on each model over the last `days` days, today among them; 0 for all of them. */
export const listUsage = (days: number) => api<UsageRow[]>(`/api/admin/usage?days=${days}`)
