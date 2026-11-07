import { Link } from '@tanstack/react-router'
import { Notice } from '../components/layout/Notice'
import { Frame } from './Frame'

export function NotFoundPage({ message = '找不到這個頁面。' }: { message?: string }) {
  return (
    <Frame title="找不到">
      <main>
        <Notice title="找不到">
          <p className="text-muted-foreground">{message}</p>
          <Link
            to="/"
            className="inline-block text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
          >
            回到 Repository 列表
          </Link>
        </Notice>
      </main>
    </Frame>
  )
}
