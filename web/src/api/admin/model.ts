import type { ChatModel, UsageRow } from '../../types/admin/model'
import { api } from '../client'

/** The chat model as it stands: its provider, name and context, and for an agent what it is. */
export const getModel = () => api<ChatModel>('/api/admin/model')

/** Each repository's spend on each model over the last `days` days, today among them; 0 for all of them. */
export const listUsage = (days: number) => api<UsageRow[]>(`/api/admin/usage?days=${days}`)
