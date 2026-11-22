import { getHealth } from '../../../api/repo/repos'
import { useApi } from '../../../lib/useApi'

/**
 * The server's health, read for its chat model: a question box stays disabled until one is known to be set.
 * unavailable says why it cannot be asked, for the box's placeholder; empty once it can.
 */
export function useChatModel() {
  const { data: health, error } = useApi('health', getHealth)
  const unavailable = error
    ? `目前無法提問：${error}`
    : health && !health.chatModel
      ? '請先設定 DEEPCODE_CHAT_MODEL 才能提問'
      : ''
  return { health, error, ready: !!health?.chatModel, unavailable }
}
