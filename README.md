# deepcode

從 GitHub、GitLab（含自架的 GitHub Enterprise 與 GitLab）匯入 Repository，建立 Symbol 與程式碼索引，生成附來源的 Wiki，並以 Hybrid Retrieval 回答程式碼問題（Ask、Deep、Codemap）。以 RBAC（使用者、角色、權限）限制每個人能看到哪些 Repository；後台管理帳號、角色與 Git 伺服器，並即時顯示背景工作的進度。另提供 MCP，讓代理程式接入。

單一 Go 執行檔：REST / SSE API、內嵌的 React 介面與背景工作都在同一個程序內，SQLite 是唯一的資料庫。支援 C#/.NET、Go、Java、JavaScript、TypeScript、Rust。

## Quick start

1. 從 [Releases](https://github.com/aitay721822/deepcode/releases) 下載 Windows amd64 的 `deepcode_<版本>_windows_amd64.exe`（`dev` 是 main 的最新開發版），或自行建置：

   ```bash
   scripts/build.sh
   ```

   建置需要 Go 1.27+ 並啟用 CGO（Tree-sitter 需要 C 編譯器）、Node.js `^20.19` 或 `>=22.12`、Bash。執行需要系統 `git` 2.31+。

2. 在執行檔旁建立 `deepcode.yaml`，填入 OpenAI 相容、支援 structured outputs 的 Chat API（語意檢索另需 Embedding API，可省略）：

   ```yaml
   chat_base_url: http://host:8080/v1
   chat_api_key: your-key
   chat_model: your-chat-model
   # embed_model: your-embedding-model
   ```

   每個鍵也可以用環境變數設定，例如 `DEEPCODE_CHAT_MODEL`，環境變數會覆寫檔案。

3. 啟動並開啟 <http://127.0.0.1:8080>：

   ```bash
   ./deepcode serve
   ```

   第一次啟動會建立管理員 `admin`，密碼取自 `DEEPCODE_ADMIN_PASSWORD`；未設定時會產生一次性密碼並印在 log。資料寫在 `./data`。`deepcode --help` 列出所有指令與旗標。

## License

[MIT](LICENSE)
