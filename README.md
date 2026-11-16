<div align="center">

# deepcode

**自架的程式碼 Wiki 與問答：讀懂任何 Repository，說明都引用到原始碼行號。**

[![CI](https://github.com/aitay721822/deepcode/actions/workflows/ci.yml/badge.svg)](https://github.com/aitay721822/deepcode/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/aitay721822/deepcode)](https://github.com/aitay721822/deepcode/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE.md)

<img src="https://cdn.jsdelivr.net/gh/aitay721822/deepcode@6c8e65c075303eeb2d65a34b10f698accaa0e60b/.github/assets/tour.gif" alt="在 go-chi/chi 的 Wiki 頁提問：deepcode 先搜尋程式碼，再串流出附行號引用的答案，右側打開引用的原始碼" width="900">

</div>

從 GitHub、GitLab（含自架的 GitHub Enterprise 與 GitLab）匯入 Repository，建立 Symbol 與程式碼索引，生成附來源的 Wiki，並以 Hybrid Retrieval 回答程式碼問題。單一 Go 執行檔，內嵌 React 介面，SQLite 是唯一的資料庫。

## 特色

- **Wiki**：依程式結構生成兩層 Wiki，附架構圖，說明引用到原始碼的行號。新的 commit 由 webhook、定時同步（`sync_every`）或手動同步帶進來，只重寫受影響的頁；要整份重建時用「重新索引」。
- **問答**：三種模式。Fast 以工具搜集程式碼後回答；Deep 搜得更多，先讀相關的 Wiki 頁；Codemap 依執行順序畫出程式路徑。檢索結合全文、Symbol 與向量。
- **語言**：C#/.NET、Go、Java、JavaScript、TypeScript、Python、Rust。
- **權限**：RBAC（使用者、角色、權限）限制每個人能看到哪些 Repository；後台管理帳號、角色、Git 伺服器，並即時顯示背景工作的進度與 LLM 用量。
- **MCP**：6 個工具（讀 Wiki、搜尋程式碼、讀檔、問答），以個人 token 或 OAuth 接入代理程式。
- **自架**：資料庫與索引都在你的機器上；Chat 與 Embedding 接任何 OpenAI 相容的 API，自架的模型也行；沒有 API 但有 Claude 或 ChatGPT 訂閱時，也能讓本機的 agent 回答（[ACP](#用訂閱的-agentacp)）。

## 截圖

**Wiki**：兩層目錄、由 import 圖畫出的架構圖，右側是同步、branch 與 webhook。

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://cdn.jsdelivr.net/gh/aitay721822/deepcode@6c8e65c075303eeb2d65a34b10f698accaa0e60b/.github/assets/wiki-dark.png">
  <img src="https://cdn.jsdelivr.net/gh/aitay721822/deepcode@6c8e65c075303eeb2d65a34b10f698accaa0e60b/.github/assets/wiki-light.png" alt="go-chi/chi 的 Wiki 首頁與架構圖">
</picture>

**問答**：答案裡的每個引用都能點開，右側直接顯示那幾行原始碼。

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://cdn.jsdelivr.net/gh/aitay721822/deepcode@6c8e65c075303eeb2d65a34b10f698accaa0e60b/.github/assets/answer-dark.png">
  <img src="https://cdn.jsdelivr.net/gh/aitay721822/deepcode@6c8e65c075303eeb2d65a34b10f698accaa0e60b/.github/assets/answer-light.png" alt="回答 chi 如何把請求路徑對應到 handler，右側是引用的原始碼">
</picture>

**Codemap**：把答案畫成依執行順序編號的程式路徑，可以切換成圖。

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://cdn.jsdelivr.net/gh/aitay721822/deepcode@6c8e65c075303eeb2d65a34b10f698accaa0e60b/.github/assets/codemap-dark.png">
  <img src="https://cdn.jsdelivr.net/gh/aitay721822/deepcode@6c8e65c075303eeb2d65a34b10f698accaa0e60b/.github/assets/codemap-light.png" alt="Mount 子路由時路由 context 如何傳遞的 code map">
</picture>

## Quick start

1. 從 [Releases](https://github.com/aitay721822/deepcode/releases) 下載 Windows amd64 的 `deepcode_<版本>_windows_amd64.exe`（`dev` 是 main 的最新開發版），或自行建置：

   ```bash
   scripts/build.sh
   ```

   建置需要 Go 1.27.1+ 並啟用 CGO（Tree-sitter 需要 C 編譯器）、Node.js `^20.19` 或 `>=22.12`、Bash。執行需要系統 `git` 2.31+。

2. 在執行檔旁建立 `deepcode.yaml`，填入 OpenAI 相容的 Chat API（語意檢索另需 Embedding API，可省略）。伺服器不必強制 JSON schema：schema 只是引導，每個回覆都由 deepcode 自己檢查、要求修正一次，剩下的由程式補正。schema 預設以 `response_format` 送出；伺服器不接受它，或要等整段 JSON 產生完才送出（例如 Strata）時，設 `chat_schema: prompt`，改把 schema 寫進系統訊息，回覆會逐字串流。

   ```yaml
   chat_base_url: http://host:8080/v1
   chat_api_key: your-key
   chat_model: your-chat-model
   # chat_api: completions  # 或 responses
   # chat_schema: server    # 或 prompt
   # embed_model: your-embedding-model
   # chat_context: 524288
   # chat_tokenizer: tokenizer.json
   ```

   每個鍵也可以用環境變數設定，例如 `DEEPCODE_CHAT_MODEL`，環境變數會覆寫檔案。

3. 啟動並開啟 <http://127.0.0.1:8080>：

   ```bash
   ./deepcode serve
   ```

   第一次啟動會建立管理員 `admin`，密碼取自 `DEEPCODE_ADMIN_PASSWORD`；未設定時會產生一次性密碼並印在 log。資料寫在 `./data`。`deepcode --help` 列出所有指令與旗標。

## 用訂閱的 agent（ACP）

**使用 ACP 時只能有一位使用者：訂閱人本人。** 使用 ACP 時，deepcode 只接一個訂閱帳號，所以也只能有一位使用者，就是這個訂閱帳號的訂閱人。你可以在 deepcode 裡開多個帳號來隔離專案，但這些帳號都必須是訂閱人自己在用。任何其他人都不可以使用屬於訂閱人的訂閱額度，不論是透過 deepcode 的網頁、MCP、API token，還是其他任何管道。這是 Anthropic 與 OpenAI 對訂閱帳號的規定（[Anthropic](https://code.claude.com/docs/en/legal-and-compliance)、[OpenAI](https://openai.com/policies/terms-of-use/)）。要讓多人使用，請改用 API key 計費的 OpenAI 相容端點（`chat_provider: openai`）。

`chat_provider: acp` 讓本機的 agent 透過 [Agent Client Protocol](https://agentclientprotocol.com) 回答，用的是它登入的訂閱：Claude Code、Codex，或 Gemini CLI 等支援 ACP 的 agent。deepcode 不碰 agent 的登入，登入一律在 agent 自己的流程完成，而且要用執行 deepcode 的同一個 OS 使用者。

1. 登入 agent（npx 需要 Node.js，Claude 的 adapter 需要 Node.js 22 以上）：Claude Code 用 `claude` 的 `/login`。Codex 給它一個 deepcode 專用的資料夾，在那裡登入一次，再把 `acp_codex_home` 設成同一個資料夾：

   ```bash
   CODEX_HOME=/path/to/deepcode-codex codex login
   ```

   Windows 的 PowerShell：

   ```powershell
   $env:CODEX_HOME = "C:\path\to\deepcode-codex"; codex login
   ```

   不設 `acp_codex_home` 時 Codex 讀你的 `~/.codex`，其中的 AGENTS.md、MCP server、plugin 與 hook 都會加進每個請求。

2. 設定（第一次執行時 npx 會先下載 agent，可以先手動跑一次）：

   ```yaml
   chat_provider: acp
   acp_command: npx -y @agentclientprotocol/claude-agent-acp
   # acp_command: npx -y @agentclientprotocol/codex-acp
   # acp_codex_home: /path/to/deepcode-codex
   # chat_model: sonnet          # agent 的 model 選項，不設時用 agent 的預設
   # reasoning_effort: high      # agent 的 thought level 選項
   # acp_sessions: 2             # 同時進行的 prompt 數
   # embed_base_url: http://host:1234/v1
   # embed_model: your-embedding-model
   ```

每個請求都是一個新的 session，在 `data/acp/work/` 下該 Repository 自己的空資料夾裡執行（不屬於任何 Repository 的請求共用一個），各 Repository 互不相通。deepcode 的指示會取代 Claude 的整個 system prompt 與 Codex 的 custom instructions。agent 拿不到 deepcode 的任何工具、檔案或終端機，它要求的權限一律被拒絕。Codex 以唯讀模式執行，沒有 shell（它要跑的指令一律被拒，而 Codex 被拒就會結束回答），回答的詳細程度用 API 預設的 medium（Codex 給自己模型的 low 回答較短、引用較少），預設不帶 `~/.agents` 的 skills 與 apps 的指示。Claude 預設不載入你的設定、CLAUDE.md、自動記憶、MCP server 與 plugin。`acp_user_config: true` 讓 Claude 載入這些，讓 Codex 帶回 `~/.agents` 的 skills 與 apps 的指示（Codex 自己的設定來自 `CODEX_HOME`；shell 照樣關、詳細程度照樣 medium，權限、環境與協作模式的說明照樣不帶）；claude.ai 的 connectors 也預設關閉，`acp_claude_connectors: true` 才打開，打開後從 session 的第二個 prompt 起，每次約多 16k token。其他 agent 的這些都由它自己決定，deepcode 關不掉：它的指示、設定、MCP server 與工具都會帶進每個 session，deepcode 的指示則放在 prompt 開頭。後台的「模型與用量」頁顯示 agent 的名稱與版本、context、Overhead 與 Scale，以及最後一次失敗（例如未登入）。agent 沒回報的 token 以本地 tokenizer 估算。

限制：

- agent 不接受 `temperature`、輸出上限與 response format：`chat_base_url`、`chat_api_key`、`chat_api`、`temperature`、`max_output_tokens` 設了會報錯，`chat_schema` 只能是 `prompt` 或不設。
- 比 API 慢。在 go-chi/chi 上，一題問答 DeepSeek（API）約 11 秒，Claude 約 35 秒，Codex（effort low）約 50 秒。wiki 一次寫 4 頁時，6 頁 Claude 約 100 秒、Codex 約 150 秒，跟 DeepSeek 一次寫 1 頁差不多。
- `data` 放在 git repository 裡時，Codex 會把從那個 repository 根目錄到工作資料夾之間的 AGENTS.md 加進每個請求。
- Claude Code 在每個請求都附上它登入帳號的 email、當天日期與環境（工作資料夾、是否在 git repository 裡、平台與 OS），deepcode 關不掉。
- agent 沒有 Embedding，向量檢索要另接 Embedding API（`embed_base_url` 與 `embed_model`，不沿用 chat 的設定）。不設時只用全文與 Symbol 檢索：小 repo 找得到的相同，較大的 repo recall@10 少到約 0.07，排序也差一些（見下表）。
- Codex 每個請求固定多出約 1.3 萬 token（它自己的指示與工具定義），Claude 約 600，所以 Codex 的額度用得快很多：在 go-chi/chi 上連跑約 7 次 wiki 與全部問答，就用完了一個 ChatGPT 訂閱的 Codex 額度。
- Codex 額度用完時，codex-acp 只回報 system error，不說原因。用執行 deepcode 的 OS 使用者、同一個 `CODEX_HOME` 跑 `codex exec "hi"`，它會說額度何時重置。
- 錯誤訊息與 log 會帶有 agent 回報的內容（例如額度何時重置）和資料夾路徑。agent 自己的輸出寫在 `data/acp/agent.log`（啟動 agent 時若已超過 10 MB，先移到 `agent.log.old`），可能引用 prompt 的內容，也就是你的程式碼。

沒有 Embedding 時的檢索，8 個 eval corpus 的 recall@10／MRR（`test/eval`）：

| Corpus | 只用全文與 Symbol | 加上向量（Qwen3 Embedding 0.6B） |
|---|---|---|
| go-chi/chi | 1.000／0.839 | 1.000／0.892 |
| pmndrs/zustand | 1.000／0.875 | 1.000／0.948 |
| expressjs/express | 1.000／0.981 | 1.000／1.000 |
| spring-petclinic | 0.964／0.737 | 1.000／0.873 |
| deepcode | 0.967／0.701 | 0.967／0.808 |
| google/gson | 0.839／0.691 | 0.911／0.748 |
| dotnet/eShop | 0.800／0.688 | 0.833／0.748 |
| tokio-rs/axum | 0.867／0.641 | 0.933／0.804 |

## 升級

1. 先備份資料庫：`./deepcode backup backup.db`，伺服器運作中也可以。
2. 停掉舊版，換上新的執行檔，再 `./deepcode serve`。資料庫在啟動時自動升級。
3. `secret_key`（`DEEPCODE_SECRET_KEY`）要沿用；沒設定時用的是 `data/secret.key`，這個檔要留著。換了 key，存下的 Git 伺服器 token 就解不開。

要退回舊版，換回舊的執行檔，並用升級前的備份取代資料庫：新版跑過的 migration 無法倒回。

## 相容性

1.0.0 起，以下只會新增，不會改名或移除；要移除的，會先在一個 minor 版本標為 deprecated 並在 log 警告，到 2.0 才移除：

- 設定：`deepcode.yaml` 的鍵、對應的 `DEEPCODE_*` 環境變數，以及 `DEEPCODE_CONFIG`。
- HTTP API 的路由，以及 MCP（`/mcp`）的工具和參數。路由與網址中資源的 id 一律是 24 字元的 CUID2（OAuth 應用的 client id 除外）；舊版的數字 id 不再有效，會回 404。
- CLI 指令 `serve`、`backup`、`passwd` 和它們的旗標。
- 資料庫：發佈過的 migration 不再修改，新版本只往前加；v0.1.0 的資料庫可以直接升級。

每一項都有測試守住清單（`TestFrozenSettings`、`TestFrozenRoutes`、`TestMCP`、`TestFrozenCommands`、`TestReleasedMigrations`），舊資料庫的升級由 `TestUpgrade` 驗證。

## License

MIT，全文見 [LICENSE.md](LICENSE.md)。
