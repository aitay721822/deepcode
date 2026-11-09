import { CopyField } from './CopyField'

const tools = [
  ['list_repositories', '列出你可讀的 Repository'],
  ['read_wiki_structure', '列出 Wiki 的頁面'],
  ['read_wiki_contents', '讀取 Wiki 頁面'],
  ['search_code', '搜尋已索引的程式碼'],
  ['read_file', '讀取檔案的指定行'],
  ['ask_question', '提問，可選 fast、deep 或 codemap 模式'],
]

/** How an agent connects to this server's /mcp: with OAuth, where the client supports it, or with a token. */
export function McpGuide() {
  const url = `${location.origin}/mcp`
  const json = JSON.stringify(
    { mcpServers: { deepcode: { url, headers: { Authorization: 'Bearer <權杖>' } } } },
    null,
    2,
  )
  return (
    <section aria-labelledby="mcp-title" className="grid gap-4">
      <h2 id="mcp-title" className="font-serif text-[24px] leading-tight font-normal tracking-[-0.015em]">
        以 MCP 連線
      </h2>
      <p className="text-sm text-muted-foreground">
        讓 Claude Code、Cursor、Codex 等代理程式透過 MCP（Streamable HTTP）讀取這裡的 Wiki、程式碼並提問。 支援 OAuth
        的用戶端第一次連線時會開啟瀏覽器，請你登入並授權；其他用戶端請在標頭帶上個人存取權杖。
      </p>
      <div className="grid gap-1.5">
        <h3 className="text-sm font-medium">伺服器網址</h3>
        <CopyField value={url} label="伺服器網址" />
      </div>
      <div className="grid gap-1.5">
        <h3 className="text-sm font-medium">Claude Code，以 OAuth 授權</h3>
        <CopyField value={`claude mcp add --transport http deepcode ${url}`} label="以 OAuth 連線的指令" />
      </div>
      <div className="grid gap-1.5">
        <h3 className="text-sm font-medium">Claude Code，以權杖連線</h3>
        <CopyField
          value={`claude mcp add --transport http deepcode ${url} --header "Authorization: Bearer <權杖>"`}
          label="以權杖連線的指令"
        />
      </div>
      <div className="grid gap-1.5">
        <h3 className="text-sm font-medium">其他用戶端的設定檔（如 Cursor 的 mcp.json）</h3>
        <CopyField value={json} label="設定" />
      </div>
      <div className="grid gap-1.5">
        <h3 className="text-sm font-medium">提供的工具</h3>
        <ul className="grid gap-1 text-sm">
          {tools.map(([name, what]) => (
            <li key={name}>
              <code className="font-mono text-[12.5px]">{name}</code>
              <span className="text-muted-foreground">：{what}</span>
            </li>
          ))}
        </ul>
      </div>
    </section>
  )
}
