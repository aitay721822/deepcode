import { Notice } from '../components/layout/Notice'
import { Button } from '../components/ui/controls/button'

/**
 * The root route's errorComponent: a render error anywhere shows this notice with a Reload instead of a blank page
 * (React logs the error). The router clears it on the next navigation (a link, Back, Forward), trying again there.
 */
export function ErrorPage() {
  return (
    <main>
      <Notice title="發生錯誤">
        <p className="text-muted-foreground">無法顯示這個頁面。</p>
        <Button variant="outline" onClick={() => location.reload()}>
          重新載入
        </Button>
        {/* A plain link: a full load, in case the page fails again. */}
        <a
          href="/"
          className="mx-auto block w-fit text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
        >
          回到 Repository 列表
        </a>
      </Notice>
    </main>
  )
}
