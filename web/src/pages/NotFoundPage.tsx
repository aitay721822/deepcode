import { Notice } from '../components/layout/Notice'
import { Link } from '../lib/router'
import { Frame } from './Frame'

export function NotFoundPage({ message = 'Page not found.' }: { message?: string }) {
  return (
    <Frame title="Not found">
      <main>
        <Notice title="Not found">
          <p className="text-muted-foreground">{message}</p>
          <Link
            to="/"
            className="inline-block text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
          >
            Back to repositories
          </Link>
        </Notice>
      </main>
    </Frame>
  )
}
