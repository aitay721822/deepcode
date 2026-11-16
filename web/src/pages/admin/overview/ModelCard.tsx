import { getModel } from '../../../api/admin/model'
import { Card } from '../../../components/ui/display/card'
import { Meta } from '../../../components/ui/display/meta'
import { LoadError } from '../../../components/ui/feedback/load-error'
import { SkeletonText } from '../../../components/ui/feedback/skeleton'
import { formatTime } from '../../../lib/format'
import { useApi } from '../../../lib/useApi'

const tokens = (n: number) => `${n.toLocaleString('zh-TW')} Token`

/**
 * The chat model as it stands: its provider, name and context; for a subscription agent also what it said it is, the
 * tokens it adds to each request and how much larger its tokens are than the local tokenizer's, and its last failure
 * since it last answered, such as not being signed in.
 */
export function ModelCard() {
  const { data, error, reload } = useApi('admin:model', getModel)
  if (error) return <LoadError error={error} onRetry={reload} />
  if (!data) return <SkeletonText />
  const agent = data.agent
  return (
    <Card className="gap-4 px-6 py-5">
      <h2 className="text-[15px] font-medium">聊天模型</h2>
      {data.provider === '' ? (
        <p className="text-[13px] text-muted-foreground">沒有設定聊天模型，Wiki 與問答都關閉。</p>
      ) : (
        <dl className="flex flex-wrap gap-x-8 gap-y-3 text-[13.5px]">
          <Meta term="提供者">{data.provider === 'acp' ? '訂閱 agent（ACP）' : 'OpenAI 相容端點'}</Meta>
          <Meta term="名稱">
            <code className="font-mono">{data.name}</code>
          </Meta>
          {agent && (
            <Meta term="Agent">
              {agent.name ? `${agent.name} ${agent.version}`.trim() : agent.trouble ? '啟動失敗' : '尚未啟動'}
            </Meta>
          )}
          {agent?.model && <Meta term="模型選項">{agent.model}</Meta>}
          <Meta term="Context">{data.context > 0 ? tokens(data.context) : '未知'}</Meta>
          {agent?.name && <Meta term="Overhead">{tokens(data.overhead)}</Meta>}
          {agent?.name && <Meta term="Scale">× {data.scale.toFixed(2)}</Meta>}
        </dl>
      )}
      {agent?.trouble && (
        <p className="text-[13px] text-destructive [overflow-wrap:anywhere]">
          最後一次失敗（{formatTime(agent.at)}）：{agent.trouble}
        </p>
      )}
      {agent && (
        <p className="text-[13px] text-muted-foreground">
          Overhead 是 agent 在每個請求自己加上的 Token，Scale 是它的 Token 數相對於本地 tokenizer
          的倍數，都在第一次啟動時量測。agent 第一次被用到時才會啟動。
        </p>
      )}
    </Card>
  )
}
