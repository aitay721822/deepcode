import { Frame } from '../../Frame'
import { AppList } from './AppList'
import { McpGuide } from './McpGuide'
import { TokenList } from './TokenList'

/** /tokens: how agents connect over MCP, the user's personal access tokens and the apps they authorized. */
export function TokensPage() {
  return (
    <Frame title="存取權杖與 MCP">
      <main className="mx-auto grid w-full max-w-[880px] gap-12 px-4 pt-9 pb-24 sm:px-6">
        <h1 className="font-serif text-[30px] leading-[1.1] font-normal tracking-[-0.02em]">存取權杖與 MCP</h1>
        <McpGuide />
        <TokenList />
        <AppList />
      </main>
    </Frame>
  )
}
