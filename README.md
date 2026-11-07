# deepcode

從 GitHub、GitLab（含自架的 GitHub Enterprise 與 GitLab）匯入 Repository，建立 Symbol 與程式碼索引，生成附來源的 Wiki，並以 Hybrid Retrieval 回答程式碼問題。以 RBAC（使用者、角色、權限）限制每個人能看到哪些 Repository 的程式碼與 Wiki；後台管理帳號、角色與 Git 伺服器，並即時顯示背景工作的進度與輸出。頁面與使用流程參考 DeepWiki。

單一 Go 執行檔：Gin 提供的 REST / SSE API、內嵌的 React SPA、Watermill 事件驅動的背景工作與記憶體快取都在同一個程序內；SQLite（WAL、FTS5、float32 向量 BLOB）是唯一的資料庫。

## 需求

| 用途 | 需求 |
|---|---|
| 建置 | Go 1.27+ 並啟用 CGO（Tree-sitter 需要 C 編譯器）、Node.js `^20.19` 或 `>=22.12`、Bash |
| 執行 | 系統 `git` 2.31+（Token 標頭以 `GIT_CONFIG_COUNT` 環境變數傳入；`go test` 也需要）、可寫入的資料目錄；問答與 Wiki 需要 OpenAI 相容的 Chat API，語意檢索另需 Embedding API |

## 建置

```bash
scripts/build.sh
```

腳本依序 `npm ci`、建置 React、把 `web/dist` 複製到 `internal/app/server/webui/dist`，再以 `go build -ldflags="-s -w"` 產生 `deepcode`（Windows 為 `deepcode.exe`）。Windows 搭配 TDM-GCC 時，未 strip 的 CGO 執行檔會被系統拒絕載入，所以 `-s -w` 不能拿掉。Tree-sitter 需要 CGO，跨平台發佈請在各目標平台建置。

## 執行

```bash
DEEPCODE_CHAT_BASE_URL=http://host:8080/v1 DEEPCODE_CHAT_API_KEY=your-key DEEPCODE_CHAT_MODEL=your-chat-model ./deepcode serve
```

開啟 <http://127.0.0.1:8080> 並登入。第一次啟動會建立管理員 `admin`：密碼取自 `DEEPCODE_ADMIN_PASSWORD`，未設定時產生一次性密碼並印在 log，登入後從右上角帳號選單變更。資料寫在 `./data`：`deepcode.db`、`repos/`（Git 抓取快取）與 `secret.key`（加密 Git 伺服器 Token 的金鑰）。

Ctrl-C（或 SIGTERM）停止服務：進行中的請求最多再等 10 秒，執行中的工作交回佇列，下次啟動接續。工作遲遲沒有停下時，再按一次 Ctrl-C（或再送一次 SIGTERM）立即結束；這樣中斷的工作下次啟動時重新排入，但算用掉一次嘗試（已是最後一次就改為失敗）。

不設模型也能匯入、索引、瀏覽檔案與搜尋 Symbol：

- 未設 `DEEPCODE_EMBED_MODEL`：只用 FTS5 與 Symbol 檢索，不計算向量。端點沒有 `/embeddings`（例如只載入聊天模型的 llama.cpp）時就不要設。FTS5 以 porter 取詞幹（`subscribers` 也找得到 `subscribe`），camelCase 識別字另外拆成單字索引（`getUserToken` 拆成 get、user、token，問題只寫 token 也找得到；問題裡的 `userToken` 也拆開來找），測試、文件與範例的分數減半，讓實作程式碼排在前面；有 Embedding 時語意檢索仍是最準的。
- 未設 `DEEPCODE_CHAT_MODEL`：不生成 Wiki，問答會回傳錯誤。

### 設定

設定都來自環境變數，`-addr`、`-data` 旗標可覆寫前兩項。數值（整數，或 `5m`、`2h` 這類時間長度）必須大於 0（`DEEPCODE_MAX_OUTPUT_TOKENS`、`DEEPCODE_LLM_RPM` 也可設 0，等於預設）；設了卻無法使用的值會讓程式拒絕啟動，並指出是哪個變數。

| 變數 | 預設 | 說明 |
|---|---|---|
| `DEEPCODE_ADDR` | `127.0.0.1:8080` | 監聽位址 |
| `DEEPCODE_DATA` | `data` | 資料目錄 |
| `DEEPCODE_CHAT_BASE_URL` | `https://api.openai.com/v1` | OpenAI 相容端點 |
| `DEEPCODE_CHAT_API_KEY` | | |
| `DEEPCODE_CHAT_MODEL` | 關閉 | 問答與 Wiki 使用的模型 |
| `DEEPCODE_EMBED_BASE_URL` | 同 Chat | |
| `DEEPCODE_EMBED_API_KEY` | 同 Chat | |
| `DEEPCODE_EMBED_MODEL` | 關閉 | 換模型後按 Sync 會重新計算向量 |
| `DEEPCODE_REASONING_EFFORT` | 伺服器預設 | 以 `reasoning_effort` 送出，例如 `none`、`low`。推理模型先想完才輸出，`none` 讓回答立刻開始串流 |
| `DEEPCODE_MAX_OUTPUT_TOKENS` | 供應商預設 | 每次回答的輸出上限；推理模型要留足夠空間 |
| `DEEPCODE_LLM_TIMEOUT` | `5m` | 單次模型請求逾時 |
| `DEEPCODE_LLM_RPM` | 不限 | 每分鐘請求上限，Chat 與 Embedding 分開計算；放棄等候的請求不佔名額 |
| `DEEPCODE_WORKERS` | `2` | 同時執行的背景工作數 |
| `DEEPCODE_JOB_TIMEOUT` | `2h` | 單一工作逾時 |
| `DEEPCODE_CACHE_MB` | `64` | 記憶體快取上限 |
| `DEEPCODE_SYNC_EVERY` | 關閉 | 每隔多久自動檢查每個 Repository 的遠端 HEAD（至少 `1m`，例如 `15m`），有新 Commit 就排索引；見「背景工作」 |
| `DEEPCODE_MAX_REPO_MB` | `1024` | Repository 大小上限：匯入時比對 Git 伺服器回報的大小（GitLab 只對有成員權限的 Token 回報）；每次下載 Commit 時，寫進快取的量超過上限就中止，該工作失敗且不重試 |
| `DEEPCODE_MAX_FILES` | `20000` | 可索引檔案數，超過則索引失敗 |
| `DEEPCODE_MAX_FILE_KB` | `512` | 超過的檔案略過 |
| `DEEPCODE_MAX_INDEX_MB` | `200` | 可索引文字總量，超過則索引失敗 |
| `DEEPCODE_WIKI_LANGUAGE` | `English` | Wiki 的撰寫語言；問答依提問的語言回答 |
| `DEEPCODE_ADMIN_PASSWORD` | 產生並印在 log | 第一次啟動時管理員 `admin` 的密碼（8 字元以上）；已有使用者後不再使用 |
| `DEEPCODE_SECRET_KEY` | `data/secret.key` | 加密 Git 伺服器 Token 的金鑰（任意字串）；未設定時使用資料目錄中第一次啟動產生的隨機金鑰 |
| `DEEPCODE_SESSION_TTL` | `720h` | 登入的有效期間 |
| `DEEPCODE_TRUSTED_PROXIES` | 無 | 反向代理的位址或 CIDR（逗號或空白分隔，例如 `127.0.0.1,10.0.0.0/8`）；來自這些位址的請求以 `X-Forwarded-For`（或 `X-Real-IP`）判斷用戶端位址，登入次數限制依此計算。代理必須把用戶端位址加進 `X-Forwarded-For`（nginx：`proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`），否則用戶端能自填位址。格式錯誤時無法啟動 |

## 使用流程

1. 以 `admin` 登入。帳號選單的 **Admin console** 管理使用者、角色與 Git 伺服器，並查看背景工作。要讓其他人自己註冊帳號，在 **Users** 打開 **Open sign-up**。
2. 首頁 **Add** 接受 `owner/repo`（第一個 Git 伺服器，預設為 GitHub）、任一已設定伺服器上的網址（例如 `https://gitlab.example.com/group/sub/project`），或選擇伺服器後輸入 `group/sub/project`。
3. Repository 頁顯示索引進度（Fetch → Parse → Embed → Publish），接著生成 Wiki（Plan → Write），頁面寫完一頁出現一頁。後台 **Jobs** 可即時看每個工作的輸出，包括模型正在寫的 Wiki 內容。
4. 左側 **Wiki** 列出頁面，**Files** 是檔案樹與 Symbol 搜尋（只有 Go、JavaScript、TypeScript / TSX 建立 Symbol 與 import 關係；其他語言以純文字切塊，仍可全文與向量檢索）。文中的來源標籤會開啟檔案並標出行號範圍；連往 GitHub 或 GitLab 的連結都綁定 Commit SHA。概覽頁附一張目錄之間的 import 關係圖，由索引的 import 直接畫出、不經模型（測試、範例與文件不算；最多 14 個目錄、24 條箭頭，目錄取到最多三層、節點仍放得下的深度）；說明架構、分層或資料流的頁面，模型會附一張 Mermaid 圖。Mermaid 第一次需要畫圖時才載入；語法錯誤、或含有會隱藏文字的字元時，顯示原文。
5. 在底部輸入問題進入問答頁：左側串流答案與來源，右側顯示被引用的程式碼。可以繼續追問，`/repos/{id}/search/{conversation}` 能重新開啟對話（只有發問者看得到）。問題裡的字詞與名稱都對不上程式碼時（例如全中文的問題），會先請模型把問題轉成英文搜尋詞與可能的識別字再找一次，多一次模型呼叫；仍無證據時回答「無法從目前程式碼確認」。每位使用者同時最多 2 個回答在生成（不論開了幾個分頁），再問會請他等其中一個寫完。提問框的 **Deep** 開關：檢索後讓模型看已找到的來源（檔案、行號、Symbol），說出還缺的程式碼要搜什麼，最多再搜 2 輪（回答 DONE、或找不到新的程式碼就停）；證據上限從 40000 提高到 64000 字元，並把同一 Repository 的 Wiki 中與問題共用字詞最多的一頁（標題中的字算三倍；中文以相鄰兩字比對；每頁都有的字不算）當背景放進提示詞，只供參考、不能引用。回答約慢 2～3 倍，檢索步驟會顯示每一輪搜尋的字詞。
6. **Sync** 取得最新 Commit（設定 `DEEPCODE_SYNC_EVERY` 後也會定期自動檢查）：未變更的檔案沿用 Symbol 與 Chunk，未變更的 Chunk 沿用向量，完成後才切換版本。保留最新兩個版本；更舊的版本分批刪除索引資料（不會長時間佔住資料庫的寫入，中斷時下次清理接續），原本綁在上面的對話改用目前版本。
7. 新版本的 Wiki 沿用上一份完成的同語言 Wiki 的頁面：引用的檔案、或以頁面標題與焦點重新檢索到的檔案（概覽頁另含 README）有新增、修改或刪除，或 import 了宣告有變的檔案時，才重寫那一頁，其餘頁面連同引用沿用，連結改指向新 Commit。「宣告有變」以 Symbol 的名稱、種類與第一行比較，只改函式內容不算，所以修改被大量引用的工具函式內部時不會牽動其他頁面；struct 新增欄位、Go interface 新增方法看不出來。重寫時已檢索不到任何程式碼的頁面（例如引用的檔案都刪了）改為說明無法從目前程式碼確認。變更的檔案（新增、修改、刪除合計）超過新版本檔案數的一半、`.devin/wiki.json` 有變更（含新增、刪除）、或新增了兩層深的目錄且其他目錄的程式碼 import 它（新的子系統）時重新規劃。判斷偏保守：有 Embedding 的小 Repository 幾乎每頁都會檢索到變更的檔案而重寫。
8. Repository 根目錄的 `.devin/wiki.json`（與 DeepWiki 同格式）可以引導 Wiki：`repo_notes` 的 `content` 在規劃與撰寫時提供給模型；列出 `pages`（`title`、`purpose`、`page_notes`）時直接寫這些頁面（最多 30 頁），不再請模型規劃，`purpose` 與 `page_notes` 作為該頁的檢索焦點。頁面是單層清單，依檔案中的順序，`parent` 不使用。JSON 格式錯誤時整份忽略，並在工作輸出說明；檔案超過 `DEEPCODE_MAX_FILE_KB`、或有一行超過 4000 位元組（例如壓成一行的 JSON）時，和其他這類檔案一樣不會被索引，也就不會生效。

   ```json
   {
     "repo_notes": [{ "content": "前端在 web/，後端在 internal/；著重 API 與資料流。" }],
     "pages": [
       { "title": "Overview", "purpose": "整體架構與進入點" },
       { "title": "HTTP API", "purpose": "router 與 handler", "page_notes": [{ "content": "列出所有路由" }] }
     ]
   }
   ```

## 帳號與權限

使用者以帳號密碼登入（PBKDF2-SHA256，600,000 次）；session 存在 HttpOnly、SameSite=Lax 的 Cookie，資料庫只存 token 的 SHA-256。每位使用者可有多個角色，每個角色是一組權限加上範圍：**全部 Repository**，或角色列出的 Repository。權限依各角色自己的範圍判斷，不會跨角色合併。

| 權限 | 範圍 | 允許 |
|---|---|---|
| `repo.read` | 角色的 Repository | 看到 Repository、Wiki、檔案與搜尋 |
| `repo.chat` | 角色的 Repository | 問答 |
| `repo.sync` | 角色的 Repository | Sync |
| `repo.import` | 全部 | 匯入；新的 Repository 會加進匯入者「有 `repo.import` 且限定 Repository」的角色 |
| `jobs.view` | 看得到的 Repository | 後台 Jobs 與即時輸出（輸出含程式碼與 Wiki 內容，所以同樣受範圍限制） |
| `users.manage` | 全部 | 管理使用者與角色（等同可以授予自己任何權限） |
| `servers.manage` | 全部 | 管理 Git 伺服器與 Token |

- 內建角色：`admin`（全部權限、全部 Repository，不能修改或刪除）、`member`（`repo.read` + `repo.chat`，Repository 由管理者指定，不能改名或刪除）。
- 註冊預設關閉。有 `users.manage` 的管理者在後台 **Users** 打開 **Open sign-up** 後，登入頁出現 **Create one**，任何能連到服務的人都能建立自己的帳號並直接登入。新帳號拿到 `member` 角色，也就是管理者給 `member` 的所有權限與 Repository，所以開放註冊時不要把管理權限加給 `member`。同一來源位址 15 分鐘內最多註冊 10 次（格式不符而被退回的不算）；在反向代理後面卻沒設定 `DEEPCODE_TRUSTED_PROXIES` 時，所有人共用這 10 次。註冊時看得出某個使用者名稱是否已被使用。
- 沒有 `repo.read` 的 Repository 一律回 404，不透露是否存在；已被匯入但看不到的 Repository 再次匯入時回 403（用改名前的舊名稱匯入也一樣，不會因此取得權限）。新 Repository 與加進匯入者角色在同一個 transaction 完成。
- 系統至少保留一位啟用中的 `admin`；不能刪除或停用自己。停用、重設密碼都會結束該使用者所有 session；當下正在進行的登入不會留下 session，進行中的變更密碼也不會蓋過重設（回應 409，請重新登入）。
- 同一使用者名稱在同一來源位址 15 分鐘內失敗 10 次後暫停登入；同一來源位址不論用哪些名稱，15 分鐘內失敗 50 次也暫停登入。變更密碼時輸錯目前密碼依使用者計數。每次嘗試在驗證密碼前就計入（成功或非密碼錯誤再退回），同時送出的大量嘗試也不會超過上限。服務放在反向代理後面時，請把代理的位址設在 `DEEPCODE_TRUSTED_PROXIES`，否則來源位址都是代理的位址：某個名稱失敗 10 次會讓所有人暫停用該名稱登入，任何名稱合計失敗 50 次會讓所有人暫停登入，各 15 分鐘。監聽非 loopback 位址又未設定時，啟動 log 會提醒。
- 忘記密碼：`read -rs pw && printf '%s\n' "$pw" | ./deepcode passwd -data ./data admin`（密碼不顯示在畫面上、也不留在 shell 歷史；服務執行中也可以）。

## Git 伺服器

後台 **Git servers** 設定 Repository 的來源：GitHub、GitHub Enterprise、GitLab.com 或自架 GitLab。第一次啟動已有 `GitHub`（`https://github.com`）。

- **Base URL** 是網頁位址，可含子路徑（`https://example.com/gitlab`）；**API URL** 留空時 GitHub 為 `https://api.github.com`、GitHub Enterprise 為 `{base}/api/v3`、GitLab 為 `{base}/api/v4`。
- **Token** 選填：沒有 Token 只能匯入公開 Repository（GitHub 未認證 API 每小時 60 次）。GitHub 需要 Contents 與 Metadata 讀取權限（classic token 為 `repo`），GitLab 需要 `read_api` 與 `read_repository`。
- API 以 `Authorization: Bearer`（GitHub）或 `PRIVATE-TOKEN`（GitLab）呼叫；`git` 以 `Authorization: Basic` 標頭經環境變數傳入，不出現在命令列，只對該 Repository 的 URL 生效，並停用 redirect。
- Token 以 AES-256-GCM 加密存放，API 不會回傳。金鑰改變（`DEEPCODE_SECRET_KEY` 或 `data/secret.key`）後須重新輸入 Token；在那之前 **Test** 會說明原因，經該伺服器匯入、同步回 409，索引工作直接失敗、不重試（重新輸入後按 **Sync** 重排）。
- Token 只送往輸入它時的位址：改 Base URL 或 API URL（包括改類型而變動的預設 API URL）時，必須重新輸入或清除 Token。
- **Test** 以 Token 呼叫 API，回報登入身分或匿名剩餘配額。
- 有 Repository 的伺服器不能刪除，也不能改類型或 Base URL。

## 背景工作（事件驅動）

背景流程以 Watermill（GoChannel Pub/Sub + Router）串接，事件只是通知，狀態一律先寫進 SQLite：

```text
POST /api/repos、Sync ──► jobs 表新增工作 ──► job.queued
每 DEEPCODE_SYNC_EVERY 輪詢 ──► 遠端 HEAD 有新 Commit ──► jobs 表新增工作 ──► job.queued
job.queued ──► worker 原子領取 ──► 索引 ──► snapshot.published
snapshot.published ──► jobs 表新增 Wiki 工作 ──► job.queued ──► worker 生成 Wiki
worker 寫進度到 jobs、輸出到 job_logs ──► job.updated / job.output ──► 後台 SSE 從 SQLite 讀取後推送
```

- `job.queued` 只是喚醒：有空的 worker 名額（`DEEPCODE_WORKERS` 個）時，就從 SQLite 依序領取可執行的工作直到沒有；名額全滿時忽略通知，由忙碌中的 worker 做完手上的工作後接手。Watermill 對每則訊息各開一個 goroutine，所以同時執行數由名額限制，不由訂閱數限制。同一個 Repository 一次只跑一個工作。
- 掃描器每 5 秒檢查一次：重試退避到期、等待同 Repository 的工作、名額全滿時被忽略的通知，以及程序啟動前就送出而遺失的通知，都會再發一次 `job.queued`。
- 已完成的工作再次被要求時（例如 force-push 回舊 Commit、Embedding 模型換回原本的）會重新排入並清除舊輸出；Wiki 已完成的版本不會重寫。
- 每個工作最多嘗試 3 次，失敗後分別等 30 秒、60 秒再重試；重試也不會好轉的錯誤（例如 Token 無法解密）直接失敗。
- 重啟時把 `running` 的工作改回 `queued`，已用完重試次數的改為 `failed`；中斷的索引從已寫入的檔案與向量接續，Wiki 從未完成的頁面接續（撰寫語言已改變時捨棄已存的頁面、重新規劃，不會混用兩種語言）。
- 工作輸出逐行寫入 `job_logs`（模型串流的 Wiki 內容每完成一行寫一行，每頁最多 1000 行，其餘只記一行說明已省略）。`job.updated`、`job.output` 只是提示（聽眾還沒取走的提示，同一個工作只留一筆，慢的聽眾不會拖住發送端），後台的 SSE 每次都從 SQLite 讀取上次之後的行，所以不會漏行或重複；斷線重連以 `Last-Event-ID` 接續，工作結束後送出 `end` 並關閉。後台的 SSE 每 20 秒送出 ping 並重新驗證 session，session 結束、帳號停用或失去權限時隨即關閉串流。
- `snapshot.published` 在程序重啟前遺失時，按 **Sync** 會補排 Wiki。
- 輪詢（`DEEPCODE_SYNC_EVERY`）依序對每個 Repository 讀取遠端 HEAD（`git ls-remote`）：HEAD 不是目前版本，且該 Commit 的索引工作不在排隊、執行中，也沒有失敗時，排入索引（曾經成功的會重排，例如 force-push 回舊 Commit）。所以失敗的工作不會被輪詢重排，超過大小上限的 Repository 不會每輪重新下載；重試、換 Embedding 模型後重算向量、補寫失敗的 Wiki 仍按 **Sync**。失敗的工作若不是該 Repository 最新的一筆，30 天後會隨工作清理刪除，之後輪詢會再試一次那個 Commit。讀不到遠端時下一輪再試，同一個錯誤（含 Git 的原始訊息）只記一次 log；某一輪發生 panic 時只記 log，不會停止服務。
- 完成超過 30 天的工作連同輸出會刪除，每個 Repository 最新的一筆保留（頁面會顯示它）；每完成一個工作刪除最多 20 筆，不會長時間佔住資料庫的寫入。

## API

除了 `health`、`auth/login`、`auth/signup`、`auth/logout`，所有路由都要登入（否則 401），並依上表的權限檢查（缺權限 403；看不到的 Repository 404）。錯誤一律是 `{"error": "..."}`。

| Method | Route | 用途 |
|---|---|---|
| `GET` | `/api/health` | 健康檢查與目前模型 |
| `POST` | `/api/auth/login` | 登入，body `{"username", "password"}`；設定 session Cookie（401 帳密錯誤、429 暫停登入） |
| `GET` | `/api/auth/signup` | `{"open": true 或 false}`：是否開放註冊 |
| `POST` | `/api/auth/signup` | 註冊，body `{"username", "displayName", "password"}`（`displayName` 可留空，即為使用者名稱）；建立 `member` 帳號並設定 session Cookie，回 201 與目前使用者（400 格式不符、403 未開放註冊、409 名稱已被使用、429 註冊次數過多） |
| `POST` | `/api/auth/logout` | 登出 |
| `GET` | `/api/auth/me` | 目前使用者、權限 |
| `POST` | `/api/auth/password` | 變更自己的密碼，body `{"current", "next"}`；結束其他 session，這個瀏覽器拿到新的 session（400 目前密碼錯誤或新密碼不符、409 期間帳號被重設、停用或刪除，新密碼未儲存、429 暫停） |
| `POST` | `/api/repos` | 匯入（`repo.import`），body `{"url": "owner/repo 或網址", "serverId": 選填}`；回應的 Repository 不含 `permissions` |
| `GET` | `/api/servers` | 可選的 Git 伺服器（`repo.import`） |
| `GET` | `/api/repos` | 看得到的 Repository，各附 `permissions`：使用者在該 Repository 上的 `repo.read`、`repo.chat`、`repo.sync` |
| `GET` | `/api/repos/{id}` | 詳細資訊、目前版本、最新工作與 `permissions` |
| `POST` | `/api/repos/{id}/sync` | 手動同步：已是最新回 200 `{"upToDate": true}`，否則 202 `{"job": …}` |
| `GET` | `/api/repos/{id}/files` | 檔案清單 |
| `GET` | `/api/repos/{id}/files/{path}` | 檔案內容 |
| `GET` | `/api/repos/{id}/symbols?q=` | Symbol 搜尋 |
| `GET` | `/api/repos/{id}/search?q=` | 混合檢索（FTS5 + Vector + Symbol → RRF；前 5 名所在檔案 import 的檔案（最多 40 個）再以全文檢索取最多 10 個 Chunk，補進最後 2 個名額，不取代前面的結果；回應的 `graph` 是補進結果的數量）。設了 Embedding 模型時，每個沒查過的查詢呼叫一次 Embedding（查詢向量最多快取一小時），不受問答的每人同時上限限制；Embedding 的呼叫速率用 `DEEPCODE_LLM_RPM` 限制（與 Chat 分開計算） |
| `GET` | `/api/repos/{id}/wiki` | Wiki 頁面與引用；概覽頁另有 `diagram`（import 關係圖的 Mermaid 原始碼，沒有 import 關係時省略） |
| `POST` | `/api/chat/stream` | SSE 問答（`repo.chat`），body `{"repoId", "conversationId", "question", "deep"}`；事件為 `conversation`、`search`（Deep 模式每多一輪檢索一次）、`evidence`、`delta`、`done`、`error` |
| `GET` | `/api/conversations/{id}` | 自己的對話紀錄 |
| `GET` | `/api/jobs/{id}` | 工作進度 |
| `GET` `POST` `PATCH` `DELETE` | `/api/admin/users[/{id}]` | 使用者（`users.manage`） |
| `PATCH` | `/api/admin/signup` | 開放或關閉註冊（`users.manage`），body `{"open": true 或 false}` |
| `GET` `POST` `PATCH` `DELETE` | `/api/admin/roles[/{id}]` | 角色（`users.manage`）；`GET /api/admin/permissions`、`GET /api/admin/repositories` 供角色編輯 |
| `GET` `POST` `PATCH` `DELETE` | `/api/admin/servers[/{id}]` | Git 伺服器（`servers.manage`）；`POST /api/admin/servers/{id}/test` 測試連線 |
| `GET` | `/api/admin/jobs?status=&limit=` | 最近的工作（`jobs.view`）；`status` 為 `queued`、`running`、`succeeded`、`failed` 之一，`limit` 為 1–500，預設 100 |
| `GET` | `/api/admin/jobs/{id}` | `{"job", "logs"}`：工作與前 5000 行輸出，其餘由 output 串流送出 |
| `GET` | `/api/admin/jobs/events` | SSE：每次工作變更送出 `job` |
| `GET` | `/api/admin/jobs/{id}/output?after=` | SSE：`log`（每行輸出）、`job`（狀態）、結束時 `end` |

`files`、`symbols`、`search` 可用 `?snapshot=` 指定仍保留的版本，預設為目前版本；`?q=` 最長 1000 bytes。

成功時回 200，除了：新增使用者、角色、Git 伺服器與註冊回 201；刪除、`auth/logout` 與 `auth/password` 回 204，沒有 body；`POST /api/repos` 匯入新的 Repository 並排入工作時回 202（之前匯入過、看得到的回 200）；同步見上表。

## 開發

```bash
go run -ldflags="-s -w" ./cmd/deepcode serve
```

```bash
npm --prefix web run dev
```

Vite（<http://localhost:5173>）把 `/api` 轉給 `127.0.0.1:8080`。測試：

```bash
go test ./...
```

```bash
npm --prefix web run typecheck
```

`go test` 需要系統 `git`。`test/e2e` 以真正的組裝（`internal/app`：Gin、Watermill、SQLite）搭配本機 Git remote、假的 GitHub / GitLab API 與假的 LLM，跑完登入、註冊、匯入、索引、Wiki、問答、角色權限、後台工作與即時輸出、增量同步（之後的 Wiki 沿用頁面、不再重新規劃）、版本清理，以及帶 Token 的自架 GitLab；`test/layout_test.go` 檢查每個資料夾最多 5 個檔案、5 個子資料夾，每個檔案最多 500 行（npm 產生的 `web/package-lock.json` 除外），以及「目錄」最後一段的依賴方向（後端看 `go list`，前端看 `web/src` 的相對 import）。

## 備份與復原

```bash
./deepcode backup -data ./data ./backup.db
```

以 `VACUUM INTO` 產生一致的快照，服務執行中也能備份，且不會覆寫既有檔案；備份檔只有擁有者可讀寫（0600），因為裡面有密碼雜湊、session 與加密的 Token。復原時停止服務，用備份檔取代 `data/deepcode.db`，刪除同目錄的 `deepcode.db-wal` 與 `deepcode.db-shm`，再啟動。`data/secret.key` 也要另外備份（或改用 `DEEPCODE_SECRET_KEY`），否則 Git 伺服器的 Token 無法解密，必須重新輸入。`data/repos/` 只是 Git 抓取快取，不必備份；檔案內容都存在 SQLite。

## 安全

- 預設只綁定 `127.0.0.1`，並拒絕非 loopback 的 `Host` 標頭（防 DNS rebinding）。綁到其他位址供團隊使用時，請在前面加上 TLS（例如反向代理並送 `X-Forwarded-Proto: https`，session Cookie 才會加上 `Secure`）。
- 除了登入、註冊與健康檢查，所有 API 都要登入並檢查角色權限；session Cookie 是 HttpOnly、SameSite=Lax，POST 與 PATCH 必須是 `application/json`（防跨站表單）。
- 每個回應都帶 `Content-Security-Policy`：腳本、連線與字型只來自本服務（樣式另可 inline，元件在執行時會加；圖片另可用 data: URI），其他網站也不能用 iframe 嵌入；另帶 `X-Content-Type-Options: nosniff` 與 `Referrer-Policy: same-origin`（點到外部網站時不送出本站網址）。所以主題的初始設定放在 `web/public/theme.js`，而不是 inline 腳本。
- 只能從後台設定的 Git 伺服器匯入；網址的每一段路徑都經過驗證，不接受帳密、query 或 `..`。Token 加密存放，只送往該伺服器的 API 與該 Repository 的 Git URL；改伺服器位址必須重新輸入 Token，API 的 redirect 只跟隨同一主機與 scheme。
- 開著的串流（後台工作、問答）每 20 秒重新驗證 session 與權限，停用帳號或移除角色後最晚 20 秒內中斷；用戶端停止讀取時，寫入在 10 秒後逾時並結束串流。讀取請求（含 body）最多 30 秒，串流不受此限。
- 500 錯誤只回一般訊息，細節（SQL、路徑、上游回應）只寫進 log；`git` 讀不到 Repository、或連不到 Git 伺服器的 API 時（502），只說明是哪一步失敗；空的 Repository 回 400。工作失敗的原因可能引用 Git 或伺服器上的路徑，完整內容只有 `jobs.view` 看得到，其他能讀該 Repository 的人只看到固定訊息。問答的錯誤只顯示使用者能處理的原因。
- 資料目錄在每次啟動時設為 0700：裡面有密碼雜湊、session、原始碼與 Token 金鑰。Windows 不認這些權限位元（Go 在 Windows 上只切換唯讀屬性，0600 的 `secret.key` 與備份檔也一樣），誰能讀取由上層資料夾繼承的 ACL 決定：資料目錄與備份要放在只有服務帳號能讀的地方，例如使用者設定檔之下；放在 `C:\data` 這類共用位置時，先用 `icacls <目錄> /inheritance:r /grant:r "%USERNAME%:(OI)(CI)F"` 只留給自己。
- `git` 以固定參數執行、不經 Shell，停用 credential helper 與互動提示，只允許 HTTP(S)，環境變數中不含 deepcode 自己的設定（金鑰、密碼）；讀取遠端 HEAD 最多等 1 分鐘，停滯 5 分鐘（或到該指令的期限，取較早者）的傳輸會中止，被終止的 `git` 留下的 remote helper 也會隨之結束。
- 不執行 Repository 內的任何程式；檔案數、單檔大小、總量、工作時間與輸出 Token 都有上限。
- 會讓文字倒序或隱形的字元（雙向控制字元、零寬空格等不可見的格式字元、韓文填充字元、Tag 字元與變體選擇符，即 Trojan Source；emoji 用的 U+FE0E、U+FE0F 與連接符除外）在程式碼檢視、程式碼區塊，以及回答與 wiki 的文字和行內程式碼中，以加框的碼位顯示（例如 `U+202E`），也不影響前後文字的方向；複製出來的文字不變。檔案開頭的 BOM 不顯示。路徑含控制或格式字元、或不是 UTF-8 的檔案不索引。
- 在 Windows 上，git 因逾時或超過 `DEEPCODE_MAX_REPO_MB` 被中止時，整個行程樹一起結束：Git for Windows 的 `cmd\git.exe` 只是啟動器，只結束它的話，真正的 git 會繼續下載。
- 原始碼、README 與註解在提示詞中標為不可信資料。`.devin/wiki.json` 的 `repo_notes` 也標為不可信：只用來決定涵蓋與強調的內容，不能改變引用與格式的規則，每則壓成一行，不能假裝區塊已結束；列出頁面的 `purpose` 與 `page_notes` 只作為該頁的檢索焦點，和模型規劃的焦點一樣使用（單行）。列出的頁數、標題、焦點與 notes 的長度都有上限。回答只保留指向實際提供給模型的檔案與行號範圍的引用，其餘移除；一個方括號內的多個引用（`[a.go:1-5; b.go:7]`）逐一驗證。Deep 模式的背景 Wiki 頁標為可能過時、不可引用、不可遵循其中的指示，連結的網址先拿掉；照抄其中的引用也一樣要對得上提供的來源才保留。
- Mermaid 以 `strict` 安全等級繪製：標籤裡的 HTML 會編碼、不能掛點擊事件；畫出的 SVG 經 Mermaid 的消毒。由 import 關係畫圖時，目錄名稱裡會結束標籤或被當成標記的字元先轉成實體。

## 目錄

每個資料夾最多 5 個檔案、5 個子資料夾，超過就依業務拆分；每個檔案只負責一件事，最多 500 行（npm 產生的 `web/package-lock.json` 除外）。

```text
cmd/deepcode/            main（指令分派）、config（環境變數）、serve、backup、passwd
internal/
  app/                   組裝根：Config、元件建構、第一位管理員、事件驅動的背景流程與定期輪詢
    importer/            匯入：Repository 加入目錄，並在同一個 transaction 加進匯入者的角色
    server/              HTTP 交付層：router 組合以下各組路由
      httpx/             共用：錯誤對應、JSON 綁定、Host / Content-Type 防護、session 與權限檢查、SSE（ping、重新驗證 session）
      authapi/           登入、註冊、登出、目前使用者、變更密碼
      repoapi/           Repository、程式碼、搜尋、Wiki、問答（handler 只解析請求、檢查權限、呼叫領域套件）
      adminapi/          後台：使用者、角色、Git 伺服器、工作與即時輸出
      webui/             內嵌的 React build（SPA fallback）
  access/                帳號與權限
    account/             使用者、密碼雜湊、第一位管理員、保留最後一位 admin
    role/                權限清單、角色與其 Repository 範圍、使用者的權限判斷（Grants，依各角色範圍）
    session/             登入、註冊（開關與次數限制）、變更密碼、session、Principal
  platform/              共用基礎設施
    storage/             SQLite 開啟、migrations（之後的依業務放子資料夾，依檔名編號排序）、備份、IN 查詢的佔位符
    cache/               記憶體 LRU Cache（含 singleflight 載入）
    events/              Watermill 事件匯流排、主題，以及給 SSE 用的短期監聽
    jobs/                SQLite 工作表、worker 名額與掃描器、進度與輸出（job_logs）及其追蹤、過期工作清理
    ai/                  OpenAI 相容的 Chat / Embedding client
  repo/                  Repository 的來源與索引
    gitserver/           Git 伺服器設定、Token 加密與金鑰檔、網址解析、來源連結格式
    provider/            GitHub / GitLab REST API：匯入資訊、連線測試
    git/                 git 指令（帶 Token 標頭的 fetch、ls-tree、cat-file）
    catalog/             Repository 目錄：匯入、同步與輪詢判斷、目前版本、檔案清單（快取）、內容與兩個版本間變更的檔案
    index/               索引
      analyzer/          語言判斷、Tree-sitter Symbol 與 import（Go、JavaScript、TypeScript / TSX）
      chunker/           Symbol-aware chunking；camelCase 識別字拆字（FTS5 的 terms 欄）
      graph/             import 解析成檔案之間的關係（Go 經 go.mod、JS/TS 經相對路徑）、被 import 的檔案、宣告變動的影響、目錄關係與 Mermaid 圖
      indexer/           增量索引、Embedding、版本發佈與清理、補齊舊 Chunk 的 terms
      vectorstore/       向量 BLOB 儲存與 Cosine 檢索
  knowledge/             由索引產生知識
    retrieval/           FTS5、Symbol、向量檢索與 RRF，再補上最佳結果 import 的程式碼
    evidence/            Evidence Pack 與引用驗證
    chat/                問答與對話、Deep 模式（多輪檢索與背景 Wiki 頁）
    wiki/                Wiki 規劃（含 .devin/wiki.json 與目錄之間的 import）、沿用上一版未受影響的頁面、撰寫與讀取
test/                    e2e、資料夾與檔案長度規則、依賴方向檢查
web/public/              theme.js：第一次繪製前套用主題（CSP 不執行 inline 腳本）
web/src/                 React + TypeScript + Vite；Tailwind CSS v4 + shadcn/ui（Radix），動畫用 motion；index.css 放主題 token（紙與墨兩套配色，訊號橘標示連結、引用與進行中）；字型隨程式打包（Fontsource 套件：Newsreader、Schibsted Grotesk、IBM Plex Mono），不向外部請求
  types/                 API 資料型別（不含執行期程式碼），分 admin/ 與 repo/
  lib/                   與業務無關的工具：router、useApi、useDialog、日期格式、URL 路徑編碼、id 檢查與 Wiki 標題錨點
    style/               cn（合併 Tailwind class）、motion 動畫預設、語法高亮
  api/                   HTTP 呼叫，每個業務一個檔案（不含 React），分 admin/ 與 repo/；client 統一處理錯誤、401 與 SSE
  components/            跨頁共用的展示元件（只吃 props，不呼叫 API）
    ui/                  shadcn/ui 元件（加上 motion）：form、controls、overlay、display、feedback
    layout/              頁首、主題切換、帳號選單、變更密碼、置中訊息
    source/              Markdown、程式碼檢視、引用標籤、串流中先行連結引用（citations.ts，與伺服器同一套語法）；唯一的元件 CSS（語法高亮配色與 Markdown 排版）
      diagram/           Mermaid 圖（第一次用到才載入，顏色取自主題 token，切換主題時重畫）
    repo/                工作進度與階段判斷（jobStage.ts）、Repository 無法開啟時的提示（RepoNotice）、來源連結格式
      provider/          Git 伺服器的名稱與標誌
    chat/                提問框、檢索步驟
  pages/                 路由頁面，各自取資料、管狀態；多頁共用的 Frame、SiteHeader、useRepo、NotFoundPage 與 ErrorBoundary 放在 pages/
    home/                Repository 列表、搜尋與加入
    repo/                Repository 頁：Wiki（wiki/）、檔案與 Symbol（files/）、同步
    search/              問答：串流回答與引用的檔案
    session/             登入與註冊頁、session（未登入時導向登入）
    admin/               後台：工作（即時列表、詳細與輸出）、使用者（含註冊開關）、角色、Git 伺服器
```

後端依賴方向：`platform` ← `repo` ← `knowledge`，`access` 不依賴其他區域；以上 ← `app/importer` ← `app/server` ← `app` ← `cmd`，不可反向；`cmd` 另直接使用 `app/server/webui`（內嵌的 UI）、`platform/storage`（開啟資料庫、`backup`）與 `access/account`（`passwd`）。`access` 與 `repo`、`knowledge` 互不引用，匯入時由 `app/importer` 串接。前端：`types` 與 `lib` 是底層（`types` 不引用其他層，`lib` 只引用 `types`）；`api` 只引用 `types`、`lib`；`components` 只引用 `types`、`lib` 與其他 `components`（不呼叫 API、不引用 `pages`）；`pages` 組合以上各層。

## 與計畫的差異

- **HTTP 框架**改用 Gin（計畫原為 `net/http`）。SSE 以手寫 `event:` / `data:` 框架輸出，維持前端解析的格式。
- **背景工作**以 Watermill GoChannel 事件驅動，SQLite `jobs` 表仍是唯一的真實狀態；事件只負責喚醒，遺失由掃描器補發。
- **增量同步**以 `git ls-tree` 的 blob SHA 比對檔案，而非 `git diff --name-status`：結果相同，也不需要舊 Commit 留在本地。
- **取得原始碼**以 shallow bare fetch 抓固定 SHA，再用 `git cat-file --batch` 讀取，不建立工作目錄。快取只保留正在索引的 Commit：換成別的 Commit 時整份重抓（depth 1 本來就會下載整棵樹，增量比對靠資料庫裡的 blob SHA），同步再多次也不會累積舊版本。下載時每 0.25 秒量一次快取大小、結束時再量一次，超過 `DEEPCODE_MAX_REPO_MB` 就中止並刪掉這份快取；抓取時關掉 Git 的自動維護，免得重新打包被算成下載量。每個 Git 指令的輸出最多保留 64 MB：上百萬個空檔案的樹只要下載幾 MB，`ls-tree` 卻要列出上百 MB，超過就讓該次索引失敗。
- **工作恢復**：資料庫只由一個程序使用，啟動時把 `running` 的工作重新排入（已用完重試次數的改為 `failed`），沒有 lease；多程序共用資料庫時才需要。
- **向量儲存**（`vectorstore.SQLite`）的 `Upsert` 一次寫多筆；沒有 `Delete`，向量隨 Chunk 以外鍵 cascade 刪除。沒有熱門向量的記憶體快取，量測到掃描成為瓶頸再加。
- **migrations** 放在 `internal/platform/storage/migrations/` 並編入執行檔；`002_fts_porter.sql` 把 FTS5 改為 porter 詞幹，並在同一個 transaction 內重建既有資料的索引。`003_access.sql`（帳號、角色、session）與 `004_git_servers_job_logs.sql`（Git 伺服器、工作輸出）讓既有資料庫升級：原有 Repository 歸到 GitHub 伺服器，原有對話歸給第一位管理員。`005_signup.sql` 新增註冊開關（預設關閉）。之後的 migration 依業務放進子資料夾（每個資料夾最多 5 個檔案），例如 `knowledge/006_wiki_language.sql`：仍記錄為 `migrations/<檔名>`、依檔名的編號排序，所以檔案搬進子資料夾不會重跑；兩個同名檔案會讓啟動失敗。`006_wiki_language.sql` 記錄每個版本的 Wiki 以哪種語言撰寫：新版本只沿用同語言 Wiki 的頁面，續寫時語言不同就重新規劃（之前的 Wiki 沒有記錄語言，不會被沿用，未完成的會重新規劃）。`repo/007_chunk_terms.sql` 為 Chunk 加上 camelCase 識別字拆開後的單字欄（`terms`），並以它重建 FTS5 索引；SQL 拆不了 camelCase，所以升級前的 Chunk 在升級後第一次啟動時於背景分批補上（中斷時下次啟動接續，補完後 log 寫出筆數），補完之前照舊只以原文比對。`repo/008_import_graph.sql` 在檔案內容旁存放它 import 的模組（`file_contents.imports`），並新增 `file_edges`（每個版本 import 解析後的檔案關係）；索引時讀取新檔案的 import，升級前的檔案在下次連結該版本時從資料庫裡的內容讀取，已發佈的版本在升級後第一次啟動時於背景連結（log 寫出版本數）。
- **Wiki 增量更新**以頁面的引用、重新檢索的結果，以及 import 了宣告有變的檔案判斷受影響的頁面；import 關係只到檔案層級，沒有呼叫圖。寧可多重寫，也不留下與程式碼不符的頁面。
- **import 解析**只認 Go 的 go.mod 模組路徑與 JS/TS 的相對路徑（含省略副檔名、`index.*`、`.js` 對應 `.ts`）；套件名稱、tsconfig 的 `paths`（如 `@/components`）與 go.work 不解析。
- **帳號與 Git 伺服器**是計畫之外新增的：計畫只匯入公開 GitHub、沒有登入機制。
- 沒有 Makefile。問答的 Deep 模式只多做最多 2 輪由模型決定字詞的檢索，不呼叫工具，也不是 DeepWiki 的 Deep Research Agent。
