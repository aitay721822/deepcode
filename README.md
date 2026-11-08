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

腳本依序 `npm ci`、以 Biome 檢查前端（`npm run check`，格式或 lint 不過就停止）、建置 React、把 `web/dist` 複製到 `internal/app/server/webui/dist`，再以 `go build -ldflags="-s -w"` 產生 `deepcode`（Windows 為 `deepcode.exe`）。Windows 搭配 TDM-GCC 時，未 strip 的 CGO 執行檔會被系統拒絕載入，所以 `-s -w` 不能拿掉。Tree-sitter 需要 CGO，跨平台發佈請在各目標平台建置。

每個語言的 grammar 是一份由 CGO 編譯的 `parser.c`，第一次建置（Go 的建置快取清空後）要多花時間，之後只重編改過的套件（約 5 秒）。在 Windows 11 + TDM-GCC 上各 grammar 單獨乾淨編譯的時間：Go 4 秒（parser.c 1.5 MB）、JavaScript 5 秒（2.7 MB）、TypeScript / TSX 7 秒（16.7 MB）、Java 4 秒（2.4 MB）、Rust 6 秒（6.2 MB）、C# 12 秒（28.3 MB）。加入 C#、Java、Rust 後，strip 過的執行檔從 36.6 MB 變成 43.3 MB。

## 執行

```bash
DEEPCODE_CHAT_BASE_URL=http://host:8080/v1 DEEPCODE_CHAT_API_KEY=your-key DEEPCODE_CHAT_MODEL=your-chat-model ./deepcode serve
```

這些設定也可以寫在 `deepcode.yaml`（見「設定」）。開啟 <http://127.0.0.1:8080> 並登入。第一次啟動會建立管理員 `admin`：密碼取自 `DEEPCODE_ADMIN_PASSWORD`，未設定時產生一次性密碼並印在 log，登入後從右上角帳號選單變更。資料寫在 `./data`：`deepcode.db`、`repos/`（Git 抓取快取）與 `secret.key`（加密 Git 伺服器 Token 的金鑰）。

Ctrl-C（或 SIGTERM）停止服務：進行中的請求最多再等 10 秒，執行中的工作交回佇列，下次啟動接續。工作遲遲沒有停下時，再按一次 Ctrl-C（或再送一次 SIGTERM）立即結束；這樣中斷的工作下次啟動時重新排入，但算用掉一次嘗試（已是最後一次就改為失敗）。

不設模型也能匯入、索引、瀏覽檔案與搜尋 Symbol：

- 未設 `DEEPCODE_EMBED_MODEL`：只用 FTS5 與 Symbol 檢索，不計算向量。端點沒有 `/embeddings`（例如只載入聊天模型的 llama.cpp）時就不要設。FTS5 以 porter 取詞幹（`subscribers` 也找得到 `subscribe`），camelCase 識別字另外拆成單字索引（`getUserToken` 拆成 get、user、token，問題只寫 token 也找得到；問題裡的 `userToken` 也拆開來找），測試、文件、範例、隱藏資料夾與產生的檔案（見「語言」）的分數降為四分之一，讓實作程式碼排在前面；有 Embedding 時語意檢索仍是最準的。
- 未設 `DEEPCODE_CHAT_MODEL`：不生成 Wiki，問答會回傳錯誤。

### 設定

設定以 [Viper](https://github.com/spf13/viper) 讀取，後面的來源覆寫前面的：內建預設值 → 設定檔 → `DEEPCODE_*` 環境變數 → `--addr`、`--data` 旗標。設成空字串的環境變數、設定檔裡的空字串都視同未設定。指令列以 [cobra](https://github.com/spf13/cobra) 建構，`deepcode --help` 與 `deepcode <指令> --help` 列出指令與旗標；旗標是兩個連字號（`-data` 會被當成 `-d -a -t -a` 而失敗）。`serve`、`backup`、`passwd` 讀同一份設定；`backup`、`passwd` 只讀資料目錄，其他設定有誤時照常執行，好讓備份與重設密碼不受影響（設定檔讀不到或格式錯誤時仍會停止；給了 `--data` 就完全不讀設定）。

設定檔可有可無：預設讀取目前目錄的 `deepcode.yaml`，不存在就略過；`DEEPCODE_CONFIG` 可指定其他路徑，指定了卻讀不到時無法啟動。副檔名決定格式（`.yaml`、`.yml`、`.json`、`.toml` 等）。檔案的鍵是下表變數去掉 `DEEPCODE_` 後的小寫，例如 `DEEPCODE_CHAT_MODEL` 寫成 `chat_model`，全部放在最上層；檔案裡有不認得的鍵（例如拼錯）時無法啟動，不會默默略過。

```yaml
chat_base_url: http://host:8080/v1
chat_api_key: your-key
chat_model: your-chat-model
reasoning_effort: none
llm_timeout: 10m
workers: 4
sync_every: 15m
trusted_proxies: [127.0.0.1, 10.0.0.0/8]
```

金鑰與密碼（`chat_api_key`、`embed_api_key`、`admin_password`、`secret_key`）可以只放在環境變數；寫進設定檔時，讓檔案只有執行服務的帳號能讀（`chmod 600 deepcode.yaml`），`.gitignore` 已排除專案根目錄的 `deepcode.yaml`。文字設定（模型、金鑰、密碼、語言等）在設定檔裡寫成數字或 `true`、`false` 時無法啟動，因為 YAML 會改寫它們（未加引號的 `0123` 讀成 83）：請加引號，例如 `admin_password: "01234567"`。數字也不要以 0 開頭：YAML 把 `workers: 010` 讀成八進位的 8。

啟動時檢查每個值，設了卻無法使用的值會讓程式拒絕啟動，並逐一列出出錯的變數（來自設定檔時列出檔名與鍵；金鑰與密碼不顯示值，URL 只顯示 scheme 與主機，其餘可能含有金鑰的部分不顯示，取不出 scheme 與主機時整個值都不顯示；設定檔格式錯誤時只顯示行號，不顯示引號內的內容）：整數必須介於 1 與 1073741824（2³⁰）之間，時間長度要寫單位（`90s`、`5m`、`2h`）且大於 0，預設為 0、不限或關閉的項目（`DEEPCODE_MAX_OUTPUT_TOKENS`、`DEEPCODE_LLM_RPM`、`DEEPCODE_SYNC_EVERY`）也可設 0，等於預設；兩個端點必須是 `http://` 或 `https://` 開頭、含主機名稱的 URL；`DEEPCODE_CHAT_API` 與 `DEEPCODE_REASONING_EFFORT` 只能是下表列出的值；`DEEPCODE_TEMPERATURE` 必須是 0 到 2 的數字。

| 變數 | 預設 | 說明 |
|---|---|---|
| `DEEPCODE_CONFIG` | `deepcode.yaml` | 設定檔路徑；只能以環境變數指定 |
| `DEEPCODE_ADDR` | `127.0.0.1:8080` | 監聽位址 |
| `DEEPCODE_DATA` | `data` | 資料目錄 |
| `DEEPCODE_CHAT_BASE_URL` | `https://api.openai.com/v1` | OpenAI 相容端點 |
| `DEEPCODE_CHAT_API_KEY` | | |
| `DEEPCODE_CHAT_MODEL` | 關閉 | 問答與 Wiki 使用的模型 |
| `DEEPCODE_CHAT_API` | `completions` | 問模型的 API：`completions`（Chat Completions，`/chat/completions`，各家相容伺服器都有）或 `responses`（Responses API，`/responses`，要求伺服器不保存回應）。兩者都串流、都送出上面的推理強度、溫度與輸出上限 |
| `DEEPCODE_EMBED_BASE_URL` | 同 Chat | |
| `DEEPCODE_EMBED_API_KEY` | 同 Chat | |
| `DEEPCODE_EMBED_MODEL` | 關閉 | 換模型後按 Sync 會重新計算向量 |
| `DEEPCODE_REASONING_EFFORT` | 伺服器預設 | 以 `reasoning_effort` 送出：`none`、`minimal`、`low`、`medium`、`high` 或 `xhigh`。推理模型先想完才輸出，`none` 讓回答立刻開始串流 |
| `DEEPCODE_TEMPERATURE` | 伺服器預設 | 以 `temperature` 送出，0 到 2。低一點（例如 `0.2`）讓 Wiki 與回答更貼近來源、每次較一致；不接受此參數的推理模型（如 OpenAI o 系列）請留空 |
| `DEEPCODE_MAX_OUTPUT_TOKENS` | 伺服器預設 | 每次回答的輸出上限；推理模型要留足夠空間。Chat Completions 以 `max_tokens` 送出（本機伺服器都認得），OpenAI 的推理模型（o 系列、GPT-5）不接受它：設了上限時改用 `DEEPCODE_CHAT_API=responses`，以 `max_output_tokens` 送出 |
| `DEEPCODE_LLM_TIMEOUT` | `5m` | 單次模型請求逾時 |
| `DEEPCODE_LLM_RPM` | 不限 | 每分鐘請求上限，Chat 與 Embedding 分開計算；放棄等候的請求不佔名額 |
| `DEEPCODE_WORKERS` | `2` | 同時執行的背景工作數 |
| `DEEPCODE_JOB_TIMEOUT` | `2h` | 單一工作逾時 |
| `DEEPCODE_CACHE_MB` | `64` | 記憶體快取上限 |
| `DEEPCODE_SYNC_EVERY` | 關閉 | 每隔多久自動檢查每個 Repository 的遠端 HEAD（至少 `1m`，例如 `15m`；`0` 為關閉），有新 Commit 就排索引；見「背景工作」 |
| `DEEPCODE_MAX_REPO_MB` | `1024` | Repository 大小上限：匯入時比對 Git 伺服器回報的大小（GitLab 只對有成員權限的 Token 回報）；每次下載 Commit 時，寫進快取的量超過上限就中止，該工作失敗且不重試 |
| `DEEPCODE_MAX_FILES` | `20000` | 可索引檔案數，超過則索引失敗 |
| `DEEPCODE_MAX_FILE_KB` | `512` | 超過的檔案略過 |
| `DEEPCODE_MAX_INDEX_MB` | `200` | 可索引文字總量，超過則索引失敗 |
| `DEEPCODE_WIKI_LANGUAGE` | `Traditional Chinese (Taiwan)` | Wiki 的撰寫語言（寫給模型看，例如 `English`）；改了之後，新版本的 Wiki 不沿用其他語言的頁面而重新規劃；問答依提問的語言回答 |
| `DEEPCODE_WIKI_PARALLEL` | `1` | 同時規劃或撰寫的 Wiki 頁數（模型端點撐得住並行時調高）；撰寫頁面時，工作輸出的每一行以 `[頁名]` 開頭 |
| `DEEPCODE_WIKI_MAX_PAGES` | `64` | 一份 Wiki 最多的頁數 |
| `DEEPCODE_ADMIN_PASSWORD` | 產生並印在 log | 第一次啟動時管理員 `admin` 的密碼（8 字元以上）；已有使用者後不再使用 |
| `DEEPCODE_SECRET_KEY` | `data/secret.key` | 加密 Git 伺服器 Token 的金鑰（任意字串）；未設定時使用資料目錄中第一次啟動產生的隨機金鑰 |
| `DEEPCODE_SESSION_TTL` | `720h` | 登入的有效期間（至少 `1m`） |
| `DEEPCODE_TRUSTED_PROXIES` | 無 | 反向代理的位址或 CIDR（逗號或空白分隔，例如 `127.0.0.1,10.0.0.0/8`；設定檔也可寫成清單）；來自這些位址的請求以 `X-Forwarded-For`（或 `X-Real-IP`）判斷用戶端位址，登入次數限制依此計算。代理必須把用戶端位址加進 `X-Forwarded-For`（nginx：`proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`），否則用戶端能自填位址。格式錯誤時無法啟動 |

## 使用流程

1. 以 `admin` 登入。帳號選單的 **管理後台** 管理使用者、角色與 Git 伺服器，並查看背景工作。要讓其他人自己註冊帳號，在 **使用者** 打開 **開放註冊**。
2. 首頁 **新增 Repository** 接受 `owner/repo`（第一個 Git 伺服器，預設為 GitHub）、任一已設定伺服器上的網址（例如 `https://gitlab.example.com/group/sub/project`），或選擇伺服器後輸入 `group/sub/project`。
3. Repository 頁顯示索引進度（Fetch → Parse → Embed → Publish），接著生成 Wiki（Plan → Write），頁面寫完一頁出現一頁。頁數依 Repository 大小而定：檔案數（最多算 16）加上檔案數的四分之一，最多 `DEEPCODE_WIKI_MAX_PAGES` 頁（例如 104 個檔案是 16 + 26 = 42 頁）。12 頁以上的 Wiki 分成兩層：程式依目錄分組（取到分組夠多的深度，只通往一個子資料夾的資料夾不算一層），彼此 import 最多的目錄（以檔案數平均）合成一節，沒有 import 時同一上層資料夾的先合，約每 6 頁一節、最多 12 節；文件（`docs/` 與其他 Markdown 指南，例如 gson 的 `UserGuide.md`）、範例、測試與工具（隱藏資料夾裡 `.github/` 這類 CI 與編輯器設定，README、CHANGELOG、CONTRIBUTING、SECURITY 這類專案說明與社群檔案，以及只有建置與設定檔的目錄，例如只剩 `pom.xml` 的子專案）依序各成一節接在程式之後，所以 manifest 不會自成一節。頁數依行數分給各節，節自己的頁面也算一頁：每節先一頁，有兩個以上目錄的節再依目錄數各多一頁，其餘逐頁給目前每行分到最少頁的節（一個檔案最多算 2000 行，程式節裡的樣式表、SQL 與設定檔只算四分之一；每多一頁至少要 100 行，約是 DeepWiki 一頁涵蓋的量，所以許多短檔案的節不會攤成彼此重述的頁面；程式節不超過程式檔數的三倍加一）；文件與範例的行數只算三分之一，最多分到總頁數的三分之一與五分之一，文件先為每份 200 行以上的指南各給一頁（最多 3 頁：指南本身就是主題）；測試與工具各最多十二分之一、且不超過 4 頁與 3 頁，合計不超過五分之一：讀者要看的是程式。只分到 2 頁的節直接寫成兩個最上層的頁面（節的總覽只會重複它們）。模型先為概覽與每一節命名（每節附最多 12 個檔案：各目錄輪流取最長的，總行數多的目錄先取，所以不會只看到依字母排在前面的子專案），再依該節的檔案（最多 200 個，同樣輪流取）與其中宣告的型別、函式規劃該節分到的頁數（主題不夠分時，較大的檔案、流程、選項與錯誤處理各成一頁；測試、範例、指南與設定檔依它們服務的功能、流程或工具分組，不會一個檔案一頁；範例（`examples/` 下的每個資料夾）、指南（每份文件）與每個測試資料夾各是一個主題，規劃的頁面標題與焦點都沒以完整字詞提到的（`basic-auth` 不算提到 `basic`），由大到小補進該節目前補得最少的頁面焦點最前面，每頁最多 2 個，以最後兩層路徑寫入（檢索只取前面的字），撰寫時焦點列出的每個範例、指南或測試資料夾各成一個 `##` 段落：每個範例都找得到，不必各占一頁）；節的頁面寫成該節的總覽，其下的頁面只寫自己的主題；`##` 段落以它說明的功能、元件或函式命名，概覽另有一段技術堆疊（語言、框架與主要依賴）。頁面的引用全都沒寫檔名（`[55-64]`，或來源的編號 `[5:1-29]`）時，附上一個正確的例子請模型再寫一次，再寫失敗時保留原本的頁面。撰寫時，同一節的頁面共用以該節標題與焦點檢索到的來源（最多約一半的篇幅），再加上自己檢索到的來源；概覽與各節的頁面先寫，其下的頁面後寫。送給模型的提示詞一律由不變到變動排列：固定的指示，這份 Wiki 共同的內容（Repository、Commit、語言、頁面清單、`.devin/wiki.json` 的說明），該節共用的來源，最後才是這一頁的要求與來源；問答則是固定的指示、先前的對話、這次的 Repository、來源與問題，最後一行要求以反引號寫出答案依據的型別、呼叫與 annotation（小模型最常改寫成描述，例如把 `JsonToken` 寫成「token 的型別」），並指定回答的語言（程式碼區塊、反引號與 「」、“”、’、"" 引起來的文字以外，有假名時是日文、有漢字時是中文，不論夾了多少英文識別字、路徑或錯誤訊息；大多是其他文字時依問題的語言；否則有 how、what、the、do 這類英文字時是英文，有其他非英文字母的字時依問題的語言；識別字與路徑（`src/a/main.go`、`x_on_y`）不算字；都看不出來時（例如「？」「Route?」）沿用對話中上一個看得出來的問題。Wiki 與來源的語言不會帶偏回答；英文問題裡沒加引號的中文會讓它以中文回答），先前的對話至少保留 6 則、累積到 12 則才一次捨棄最舊的 6 則。這樣模型服務的提示詞快取（DeepSeek 與 OpenAI 的 prefix cache、llama.cpp 的 `cache_prompt`）能重用前段，計費的輸入 token 較少。較小的 Repository 照舊是 4～8 頁的單層清單。後台 **工作** 可即時看每個工作的輸出，包括模型正在寫的 Wiki 內容。
4. 左側 **Wiki** 列出頁面（節的頁面編號 2、3…，其下的頁面縮排、編號 2.1、2.2…），**檔案** 是檔案樹與 Symbol 搜尋（建立 Symbol 與檔案關係的語言見「語言」；其他語言以純文字切塊，仍可全文與向量檢索）。文中的來源標籤會開啟檔案並標出行號範圍；連往 GitHub 或 GitLab 的連結都綁定 Commit SHA。概覽頁附一張目錄之間的 import 關係圖，由索引的 import 直接畫出、不經模型，測試、範例、文件與隱藏資料夾不算；最多 14 個目錄、24 條箭頭，目錄取到最多三層、節點仍放得下的深度。分層時，只通往一個子資料夾、本身沒有程式的資料夾併入下一層；所有程式都在其下的資料夾（例如只有 `src/` 有程式時的 `src/`，或 Java 的 `src/main/java/org/acme/app/`）不算一層。標籤省略各節點共同的上層資料夾，三個以上併成一層的資料夾只寫頭尾（`gson/…/gson/internal`）。說明架構、分層或資料流的頁面，模型會附一張 Mermaid 圖。Mermaid 第一次需要畫圖時才載入；語法錯誤、或含有會隱藏文字的字元時，顯示原文。
5. 在底部輸入問題進入問答頁：左側串流答案與來源，右側顯示被引用的程式碼。可以繼續追問，`/repos/{id}/search/{conversation}` 能重新開啟對話（只有發問者看得到）。問題裡的字詞與名稱都對不上程式碼時（例如全中文的問題），會先請模型把問題轉成英文搜尋詞與可能的識別字再找一次，多一次模型呼叫；仍無證據時回答「無法從目前程式碼確認」。每位使用者同時最多 2 個回答在生成（不論開了幾個分頁），再問會請他等其中一個寫完。提問框的 **Deep** 開關：檢索後讓模型看已找到的來源（檔案、行號、Symbol），說出還缺的程式碼要搜什麼，最多再搜 2 輪（回答 DONE、或找不到新的程式碼就停）；證據上限從 40000 提高到 64000 字元，並把同一 Repository 的 Wiki 中與問題共用字詞最多的一頁（標題中的字算三倍；中文以相鄰兩字比對；每頁都有的字不算）當背景放進提示詞，只供參考、不能引用。回答約慢 2～3 倍，檢索步驟會顯示每一輪搜尋的字詞。不論是否 Deep，答案寫完先檢查一次：說了某段程式碼沒提供（系統提示要求寫成「not in the provided sources」「不在提供的來源中」，或整題無法回答時的「This can't be confirmed from the current code」「無法從目前程式碼確認」，也接受「I can't confirm this from the current code」這種主動句；完整的答案談程式碼、不談拿到了什麼，所以只能指來源的說法（「the provided sources」「提供的來源」「你提供的片段」；「user-provided sources」、搜尋引擎的「retrieved sources」、「使用者提供的來源」、答案自己的「上述程式碼」、可能是在評論程式碼的「你提供的程式碼」不算）在同一子句裡被否定經由「in」「from」「中」指到，或開頭說它沒有包含什麼，都算，例如「No handler was found in the provided sources」「The provided sources say nothing about retries」「提供的來源中並無付款的處理」；可能另有所指的「the provided code」（授權碼）、「these sources」（CORS 來源）、單獨的「提供的程式碼」（模組提供的程式碼）只認規定的句型，且來源一詞須結束子句，所以「if the token is not provided, the code returns 401」「not in the sources directory」「not in the source files」不算），或在行內程式碼中提到 Repository 有定義、但來源裡沒有的名稱（例如 `RoutePattern`），就再搜一輪：那些名稱，加上請模型就缺的部分說出的類別、函式或 handler；並沿 import 關係找出 import 了定義這些名稱（由兩個以上單字組成的）的檔案、且用到它們的程式碼（例如事件的 handler）。重寫時答案引用過的來源與新找到的輪流排在最前，其餘在後：證據有字數上限，兩者都不會被對方整個擠掉；答案自言自語（「Wait」「Let me check」「等等」這類句子兩句以上）時也以同樣的來源重寫。重寫的答案再檢查一次，最多重寫兩次；畫面上原本的答案被清掉，重新串流。檢查或重寫失敗（例如模型沒回應）時保留原本的答案。答案中寫在方括號外（`a.go:10-20`、` `a.go:10-20` `）或程式碼區塊裡的行號範圍，對得上來源時也算引用（程式碼區塊裡的列在答案最後的「來源：」）；方括號內加了 `source:` 標籤、或路徑前多了資料夾（`src/lib/express.js` 指提供的 `lib/express.js`，取最長的相符路徑，且提供的路徑本身要在資料夾裡：根目錄的 `main.go` 不算 `cmd/foo/main.go`）也算；最後仍沒有任何有效引用時，答案結尾註明沒有引用任何已索引的原始碼。
6. **同步** 取得最新 Commit（設定 `DEEPCODE_SYNC_EVERY` 後也會定期自動檢查）：未變更的檔案沿用 Symbol 與 Chunk，未變更的 Chunk 沿用向量，完成後才切換版本。保留最新兩個版本；更舊的版本分批刪除索引資料（不會長時間佔住資料庫的寫入，中斷時下次清理接續），原本綁在上面的對話改用目前版本。
7. 新版本的 Wiki 沿用上一份完成的同語言 Wiki 的頁面，只重寫這次變更會讓它讀起來不同的頁面，其餘頁面連同引用沿用。被引用、且有變更的檔案逐行比對新舊版本（Myers 最短編輯；兩端相同的行之間差異超過 1000 處時，中間全算被取代）：頁面引用的行範圍裡有一行被改或刪、或中間插入了新行，或引用的檔案 import 了宣告被刪改的檔案時，才重寫那一頁；沒被打斷的引用跟著行移動（例如上方插入 2 行，`a.go:40-50` 變成 `a.go:42-52`），頁面裡的連結一併改成新行號、指向新 Commit。以頁面標題與焦點重新檢索（不含該節共用的來源）時，修改過的檔案只有出現新的 Chunk（這個檔案之前沒有的 Symbol，例如新的方法或 README 新的標題）才算，原本就有的 Chunk 只是內容或位置變了不算；新增的檔案只影響它所屬分組裡檢索得到它的頁面：分組是一節（或不在節中的一頁），新檔案屬於引用的檔案與它共用最深目錄的分組（根目錄算一層；沒有分組引用同一個頂層目錄的檔案時不屬於任何分組）。`package.json`、`go.mod`、`Cargo.toml`、`pom.xml`、`build.gradle`、`*.csproj`、`*.sln` 這類 manifest 或設定檔，引用的行被打斷了還要頁面文字提到被改掉或刪掉的字（不分大小寫，例如升級前的版本號 `2.3.0`；一段被取代的行與取代它的行數相同時逐行比，否則整段比）才重寫，重新檢索到 manifest 不算；例如只升級一個相依套件的版本，不會重寫引用 scripts 的頁面，也不會重寫引用了相依套件清單、但沒寫出舊版本號的頁面。「宣告被刪改」以 Symbol 的名稱、種類與第一行比較，舊版的宣告新版沒有了才算：只改函式內容、或只新增宣告（之前 import 它的程式用不到）都不算，所以修改被大量引用的工具函式內部、或新增一個常數，不會牽動其他頁面；struct 新增欄位、Go interface 新增方法看不出來。重寫時已檢索不到任何程式碼的頁面（例如引用的檔案都刪了）改為說明無法從目前程式碼確認。變更的檔案（新增、修改、刪除合計）超過新版本檔案數的一半、`.devin/wiki.json` 有變更（含新增、刪除）、新增了兩層深的目錄且其他目錄的程式碼 import 它（新的子系統）、或 Wiki 是單層而 Repository 的大小已要分節（或反過來；頁面由 `.devin/wiki.json` 列出時不算）時重新規劃。
8. Repository 根目錄的 `.devin/wiki.json`（與 DeepWiki 同格式）可以引導 Wiki：`repo_notes` 的 `content` 在規劃與撰寫時提供給模型；列出 `pages`（`title`、`purpose`、`page_notes`、`parent`）時直接寫這些頁面（最多 30 頁），不再請模型規劃，`purpose` 與 `page_notes` 作為該頁的檢索焦點。`parent` 是另一頁的標題時，該頁放在那一頁之下；Wiki 只有兩層，所以孫頁面放在最上層的祖先之下，`parent` 對不到任何一頁、或互相指向成環時放在最上層。最上層的頁面依檔案中的順序，各自接著其下的頁面。JSON 格式錯誤時整份忽略，並在工作輸出說明；檔案超過 `DEEPCODE_MAX_FILE_KB`、或有一行超過 4000 位元組（例如壓成一行的 JSON）時，和其他這類檔案一樣不會被索引，也就不會生效。

   ```json
   {
     "repo_notes": [{ "content": "前端在 web/，後端在 internal/；著重 API 與資料流。" }],
     "pages": [
       { "title": "Overview", "purpose": "整體架構與進入點" },
       { "title": "HTTP API", "purpose": "router 與 handler", "page_notes": [{ "content": "列出所有路由" }] }
     ]
   }
   ```

## 語言

| 語言 | Symbol | 檔案之間的關係 |
|---|---|---|
| Go | function、method（掛在 receiver 的型別上）、struct、interface、type | import 路徑經 go.mod 的模組路徑對到套件目錄的每個檔案 |
| JavaScript、TypeScript / TSX | function、class 與 method、interface、type、enum、namespace | 以 `.` 開頭的 import、export from、require()、import()（可省略副檔名、`index.*`，`.js` 也對到 `.ts`） |
| C#（.NET Core 與 .NET Framework） | namespace（含 file-scoped）、class、struct、record、interface、enum、delegate；method、constructor、property、indexer、event、operator | `using`（含 `static`、別名）、`global using` 與專案檔的 `<Using>` 套用到同一個專案（最近的 .csproj）的每個檔案；同一個 namespace 的型別不必 using，所以收集每個檔案用到的型別名稱（繼承、欄位與參數型別、new、泛型參數、attribute、靜態呼叫），依序在自己與上層的 namespace、using 的 namespace、專案的 global using 裡找宣告它的檔案（partial class 連到每個檔案；每個檔案最多 50 條）；.csproj、.vbproj、.fsproj 的 ProjectReference 連成專案之間的關係 |
| Java | class、interface、enum、record、annotation；method、constructor | `import`（單一型別、`*`、`static`）；同一個 package 的型別不必 import，依檔案宣告的 package 找，不看 src/main/java 這類路徑（每個檔案最多 50 條） |
| Rust | fn、struct、enum、union、trait（方法掛在 trait 下）、type、macro_rules、有內容的 mod；`impl` 與 `impl Trait for Type` 裡的方法掛在型別上 | `mod x;` 對到 x.rs 或 x/mod.rs；`use` 與程式中的 `crate::`、`super::`、`self::` 路徑，依最近的 Cargo.toml 找到 crate 根（src/lib.rs、src/main.rs、`[lib]`、`[[bin]]`）再沿模組樹解析；workspace 成員之間以 crate 名稱解析（`-` 換成 `_`）；外部 crate 不解析 |

文件註解（`///`、Javadoc、`//`）、C# 的 `[Attribute]`、Java 的 annotation 與 Rust 的 `#[derive]` 都算在 Symbol 的行號範圍內。超過 120 行的 class、trait 依成員切塊；namespace（C# 與 TS 的 namespace、Rust 的 inline `mod`）一律依成員切塊（Chunk 以裡面的型別命名，不以 namespace），`impl` 裡的方法各自成塊。Symbol 搜尋不分大小寫，只有 C# 的 property、event、indexer 與 constructor 要拼得和程式碼一樣才找得到：它們常以名詞命名（`Items`、`Order`），問題裡的一般字詞不該找到它們。

**VB.NET** 只以純文字索引：唯一的 grammar 是社群的 CodeAnt-AI/tree-sitter-vb-dotnet，它的 Go binding 呼叫 `tree_sitter_tree_sitter_vb_dotnet`，parser.c 卻只定義 `tree_sitter_vb_dotnet`，CGO 編譯不過；直接編譯 parser.c 也會把 `Inherits`、`Implements`、`As New List(Of T)`、`Imports A = X.Y` 解析成錯誤節點。.vbproj 的 ProjectReference 仍會連成專案之間的關係。

.NET 的專案與設定檔照純文字索引：.csproj、.vbproj、.fsproj、.sln、.slnx、.props、.targets、packages.config、web.config、app.config 等 `*.config`、NuGet.config、global.json、.resx、.xaml、.cshtml、.razor、.aspx、.ascx、.asax、.asmx；Java 的 `.properties` 與 JMeter 的測試計畫 `.jmx` 也是。

**產生的檔案**照純文字索引，不建 Symbol 與 import，不進關係圖與 Wiki 的規劃，檢索分數降為四分之一：`*.Designer.cs` 與 `*.Designer.vb`（含 Migrations 裡的）、`*.g.cs`、`*.g.i.cs`、`*.AssemblyInfo.cs`、`AssemblyInfo.cs` 與 `AssemblyInfo.vb`、Service References 與 Connected Services 裡的程式，以及開頭 2 KB 內有標記的檔案：C#、VB.NET、F# 看 `<auto-generated`（例如 EF Core 的 ModelSnapshot），其他程式碼看 Go 的 `// Code generated ... DO NOT EDIT.`（Markdown 與純文字不看，文件裡引用標記不算）。

**測試、範例、benchmark、文件與隱藏資料夾**的檢索分數降為四分之一、不進關係圖：Go 的 `*_test.go`、JS/TS 的 `.test.`、`.spec.`、`__tests__/`、Python 的 `test_*`、C# 的 `*Tests.cs`、`*Test.cs` 與名稱以 Tests 結尾的專案資料夾（`Ordering.UnitTests/`）、Java 的 `src/test/`、`*Test.java`、`*Tests.java`、`*IT.java`、Rust 的 `tests/`、`benches/`，以及 `test/`、`tests/`、`spec/`、`e2e/`、`docs/`、`examples/`、`_examples/`、Markdown 與隱藏資料夾（`.github/` 這類 CI 與編輯器設定）。

**不索引**的資料夾：`node_modules`、`vendor`、`third_party`、`dist`、`coverage`、`.gradle` 等。`bin/`、`obj/`、`target/`、`build/`、`packages/` 只有在裝的是建置輸出時才略過（`bin/Debug`、`obj/x64/Release` 這種 .NET 的組態資料夾，大小寫照 .NET 的寫法；`target/debug`、`target/release`、`target/classes` 等 Cargo 與 Maven 輸出，`build/classes`、`build/generated` 等 Gradle 輸出，兩者都只在 `src/` 外面；`packages/Newtonsoft.Json.13.0.3` 這種 NuGet 套件資料夾；任何地方的 `*.dll.config`、`*.exe.config` 與 `obj/` 裡 NuGet restore 的 `project.assets.json`、`*.csproj.nuget.*`），因為 Rust 的 `src/bin/debug/`、Go 的 `obj` 套件、Java 的 `target` 套件、放腳本的 `build/` 與 JS monorepo 的 `packages/` 都是原始碼。

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
- 註冊預設關閉。有 `users.manage` 的管理者在後台 **使用者** 打開 **開放註冊** 後，登入頁出現 **建立帳號**，任何能連到服務的人都能建立自己的帳號並直接登入。新帳號拿到 `member` 角色，也就是管理者給 `member` 的所有權限與 Repository，所以開放註冊時不要把管理權限加給 `member`。同一來源位址 15 分鐘內最多註冊 10 次（格式不符而被退回的不算）；在反向代理後面卻沒設定 `DEEPCODE_TRUSTED_PROXIES` 時，所有人共用這 10 次。註冊時看得出某個使用者名稱是否已被使用。
- 沒有 `repo.read` 的 Repository 一律回 404，不透露是否存在；已被匯入但看不到的 Repository 再次匯入時回 403（用改名前的舊名稱匯入也一樣，不會因此取得權限）。新 Repository 與加進匯入者角色在同一個 transaction 完成。
- 系統至少保留一位啟用中的 `admin`；不能刪除或停用自己。停用、重設密碼都會結束該使用者所有 session；當下正在進行的登入不會留下 session，進行中的變更密碼也不會蓋過重設（回應 409，請重新登入）。
- 同一使用者名稱在同一來源位址 15 分鐘內失敗 10 次後暫停登入；同一來源位址不論用哪些名稱，15 分鐘內失敗 50 次也暫停登入。變更密碼時輸錯目前密碼依使用者計數。每次嘗試在驗證密碼前就計入（成功或非密碼錯誤再退回），同時送出的大量嘗試也不會超過上限。服務放在反向代理後面時，請把代理的位址設在 `DEEPCODE_TRUSTED_PROXIES`，否則來源位址都是代理的位址：某個名稱失敗 10 次會讓所有人暫停用該名稱登入，任何名稱合計失敗 50 次會讓所有人暫停登入，各 15 分鐘。監聽非 loopback 位址又未設定時，啟動 log 會提醒。
- 忘記密碼：`read -rs pw && printf '%s\n' "$pw" | ./deepcode passwd --data ./data admin`（密碼不顯示在畫面上、也不留在 shell 歷史；服務執行中也可以）。

## Git 伺服器

後台 **Git 伺服器** 設定 Repository 的來源：GitHub、GitHub Enterprise、GitLab.com 或自架 GitLab。第一次啟動已有 `GitHub`（`https://github.com`）。

- **基底網址** 是網頁位址，可含子路徑（`https://example.com/gitlab`）；**API URL** 留空時 GitHub 為 `https://api.github.com`、GitHub Enterprise 為 `{base}/api/v3`、GitLab 為 `{base}/api/v4`。
- **存取 Token** 選填：沒有 Token 只能匯入公開 Repository（GitHub 未認證 API 每小時 60 次）。GitHub 需要 Contents 與 Metadata 讀取權限（classic token 為 `repo`），GitLab 需要 `read_api` 與 `read_repository`。
- **驗證方式** 選 **存取 Token**（貼上 Token）或 **OAuth 授權**（不必貼 Token）。OAuth：先在 GitHub（Settings → Developer settings → OAuth Apps，或 GitHub App）或 GitLab（Applications）建立 OAuth 應用程式，回呼網址填對話框顯示的 `<deepcode 網址>/api/admin/oauth/callback`，GitLab 的權限範圍選 `read_api` 與 `read_repository`（GitHub 要求 `repo`，OAuth App 沒有更窄的私有 Repository 讀取範圍）；在對話框填入 Client ID 與 Client Secret 並儲存，再在列表按 **授權**，瀏覽器會到該伺服器的同意頁，同意後回到後台，取得的 Token 加密存放，之後的 API 與 `git` 都使用它。會過期的 Token（GitLab 兩小時、啟用過期的 GitHub App 八小時）在使用前一分鐘內以 refresh token 自動換新（同一時間只換一次，GitLab 的 refresh token 只能用一次）；refresh token 也失效時，用到它的動作會說明須 **重新授權**；權杖端點沒有回應或回應 5xx 時則說明稍後再試（授權仍有效，背景工作照常重試，**測試連線** 顯示同一訊息）。授權流程以隨機 state 綁定開始授權的管理員，10 分鐘內有效、只能用一次（存在記憶體，服務重啟後須重新授權）；回呼網址依瀏覽器連到 deepcode 的網址推得（反向代理以 `X-Forwarded-Proto: https` 告知 HTTPS）。換 OAuth 應用程式、改位址、或在 Token 與 OAuth 之間切換都會結束原本的授權；勾選 **取消授權** 移除授權取得的 Token。只有 Git 伺服器用 OAuth，deepcode 本身的登入仍是帳號密碼。
- API 以 `Authorization: Bearer`（GitHub，以及 GitLab 的 OAuth Token）或 `PRIVATE-TOKEN`（GitLab 貼上的 Token）呼叫；`git` 以 `Authorization: Basic` 標頭經環境變數傳入，不出現在命令列，只對該 Repository 的 URL 生效，並停用 redirect。
- Token、Client Secret 與 refresh token 以 AES-256-GCM 加密存放，API 不會回傳。金鑰改變（`DEEPCODE_SECRET_KEY` 或 `data/secret.key`）後須重新輸入 Token；在那之前 **測試連線** 會說明原因，經該伺服器匯入、同步回 409，索引工作直接失敗、不重試（重新輸入後按 **同步** 重排）。
- Token 只送往輸入它時的位址：改基底網址或 API URL（包括改類型而變動的預設 API URL）時，必須重新輸入或清除 Token；OAuth 則須重新輸入 Client Secret 並重新授權。
- **測試連線** 以 Token 呼叫 API，回報登入身分或匿名剩餘配額。
- 有 Repository 的伺服器不能刪除，也不能改類型或基底網址。

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
- `snapshot.published` 在程序重啟前遺失時，按 **同步** 會補排 Wiki。
- 輪詢（`DEEPCODE_SYNC_EVERY`）依序對每個 Repository 讀取遠端 HEAD（`git ls-remote`）：HEAD 不是目前版本（或是目前版本、但由舊版分析器讀的），且該 Commit 的索引工作不在排隊、執行中，也沒有失敗時，排入索引（曾經成功的會重排，例如 force-push 回舊 Commit，或升級後就地重新分析）。所以失敗的工作不會被輪詢重排，超過大小上限的 Repository 不會每輪重新下載；重試、換 Embedding 模型後重算向量、補寫失敗的 Wiki 仍按 **同步**。失敗的工作若不是該 Repository 最新的一筆，30 天後會隨工作清理刪除，之後輪詢會再試一次那個 Commit。讀不到遠端時下一輪再試，同一個錯誤（含 Git 的原始訊息）只記一次 log；某一輪發生 panic 時只記 log，不會停止服務。
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
| `GET` | `/api/repos/{id}/search?q=` | 混合檢索（FTS5 + Vector + Symbol + 查詢點名的檔案 → RRF；查詢寫出的路徑（`src/app.ts`）、資料夾（`examples/`）、glob（`tests/*.rs`）或檔名（`App.java`）所指的檔案，其 Chunk 依查詢的字另排一份名單一起融合，名次與全文檢索的同等計分（查詢點名的就是它要的；權重調低時，每個專案的 `Extensions.cs` 又被 Symbol 檢索找到，會再把 eShop 的 `AppHost.cs` 擠出它的頁面；每個檔案先取最好的一個再輪下一個，每檔最多 3 個、全部最多 6 個，免得問到 `package.json` 的問題被它的 Chunk 佔滿；在這些點名檔案裡依查詢的前 16 個檢索詞（含拆開的識別字）排出的前 25 個 Chunk 都沒有它的點名檔案，在已找到與已補上的檔案合計未滿 6 個時取它的第一個 Chunk；回應的 `named` 是這份名單的數量）；一個查詢最多點名 16 個名稱，一個名稱指到的檔案超過剩下的名額（共 40 個）就不算，資料夾含其下各層的檔案，只寫檔名而有好幾個、根目錄又沒有的（每個專案都有的 `appsettings.json`）也不算；只寫檔名時要是有索引的語言的副檔名，網址不算，`a.go:42`、`a.go#L42` 這類行號，包住名稱的引號、括號與粗體，以及開頭的 `./` 都會去掉：Wiki 頁面的 focus 會列出它談的檔案，光靠字面常排不進來（eShop 的 `AppHost.cs` 輸給每個專案的 `Extensions.cs`）；前 5 名所在檔案 import 的檔案（最多 40 個）再以全文檢索取最多 10 個 Chunk，補進最後 2 個名額，不取代前面的結果；回應的 `graph` 是補進結果的數量。命中檔頭（第一個宣告之前的授權、package 與 import）時，與緊接的宣告 Chunk 合成一筆，排在兩者較前的名次：檔頭本身沒有程式，它的 import 卻常讓它勝出）。設了 Embedding 模型時，每個沒查過的查詢呼叫一次 Embedding（查詢向量最多快取一小時），不受問答的每人同時上限限制；Embedding 的呼叫速率用 `DEEPCODE_LLM_RPM` 限制（與 Chat 分開計算） |
| `GET` | `/api/repos/{id}/wiki` | Wiki 頁面與引用；概覽頁另有 `diagram`（import 關係圖的 Mermaid 原始碼，沒有 import 關係時省略），節之下的頁面另有 `parent`（所屬節頁面的 slug） |
| `POST` | `/api/chat/stream` | SSE 問答（`repo.chat`），body `{"repoId", "conversationId", "question", "deep"}`；事件為 `conversation`、`search`（Deep 模式每多一輪檢索一次）、`evidence`、`delta`、`done`、`error`；答案被檢查後重寫時，再送一次 `search`（有多搜一輪時）、`evidence` 與 `delta`，前端收到 `evidence` 就清掉已顯示的答案；重寫失敗時，在重寫已送出的事件（`search`、`evidence`，可能還有部分 `delta`）之後再送一次 `evidence`（原本答案的來源），之後不再有 `delta`，`done` 帶回原本的答案 |
| `GET` | `/api/conversations/{id}` | 自己的對話紀錄 |
| `GET` | `/api/jobs/{id}` | 工作進度 |
| `GET` `POST` `PATCH` `DELETE` | `/api/admin/users[/{id}]` | 使用者（`users.manage`） |
| `PATCH` | `/api/admin/signup` | 開放或關閉註冊（`users.manage`），body `{"open": true 或 false}` |
| `GET` `POST` `PATCH` `DELETE` | `/api/admin/roles[/{id}]` | 角色（`users.manage`）；`GET /api/admin/permissions`、`GET /api/admin/repositories` 供角色編輯 |
| `GET` `POST` `PATCH` `DELETE` | `/api/admin/servers[/{id}]` | Git 伺服器（`servers.manage`）；`POST /api/admin/servers/{id}/test` 測試連線；`GET /api/admin/servers/{id}/oauth` 轉到該伺服器的 OAuth 同意頁，`GET /api/admin/oauth/callback` 是同意後的回呼，完成後轉回 `/admin/servers?oauth=`（`ok` 或失敗原因） |
| `GET` | `/api/admin/jobs?status=&limit=` | 最近的工作（`jobs.view`）；`status` 為 `queued`、`running`、`succeeded`、`failed` 之一，`limit` 為 1–500，預設 100 |
| `GET` | `/api/admin/jobs/{id}` | `{"job", "logs"}`：工作與前 5000 行輸出，其餘由 output 串流送出 |
| `GET` | `/api/admin/jobs/events` | SSE：每次工作變更送出 `job` |
| `GET` | `/api/admin/jobs/{id}/output?after=` | SSE：`log`（每行輸出）、`job`（狀態）、結束時 `end` |

`files`、`symbols`、`search` 可用 `?snapshot=` 指定仍保留的版本，預設為目前版本；`?q=` 最長 1000 bytes。

Repository 的 `{id}`，以及 `repoId`（問答、對話、後台工作）與 `repositoryIds`（角色）裡的值，是匯入時產生的 CUID2：24 個字元，小寫字母開頭、其後為小寫字母與數字（例如 `tz4a98xxat96iws9zmbrgj3a`）。資料庫的流水號找不到 Repository：`/api/repos/1` 與 `{"repoId": "1"}` 回 404，角色裡的 `"1"` 回 400。工作、對話、使用者、角色與 Git 伺服器的 `{id}` 仍是整數。

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

```bash
npm --prefix web run check
```

前端的格式與 lint 只用 Biome（`@biomejs/biome`，版本鎖定）：`check` 一次檢查格式、lint 規則與 import 排序，不改檔案；`fix` 套用格式、import 排序與安全的修正；`format` 只排版，`lint` 只跑規則。設定在 `web/.config/biome.jsonc`（`web/` 已有 5 個檔案），所以每個 script 都帶 `--config-path=.config`；編輯器的 Biome 擴充套件也要指向這個路徑。規則用 Biome 建議的一組加上 React 規則；關掉或放寬的規則在設定檔裡寫明理由，個別例外用 `biome-ignore` 註解並寫明原因。

`go test` 需要系統 `git`。`test/e2e` 以真正的組裝（`internal/app`：Gin、Watermill、SQLite）搭配本機 Git remote、假的 GitHub / GitLab API 與假的 LLM，跑完登入、註冊、匯入、索引、Wiki、問答、角色權限、後台工作與即時輸出、增量同步（之後的 Wiki 沿用頁面、不再重新規劃）、版本清理，以及帶 Token 的自架 GitLab；`test/layout_test.go` 檢查每個資料夾最多 5 個檔案、5 個子資料夾，每個檔案最多 500 行（npm 產生的 `web/package-lock.json` 除外），以及「目錄」最後一段的依賴方向（後端看 `go list`，前端看 `web/src` 的相對 import）。

### 檢索評測

`test/eval` 用答案已知的問題衡量檢索：把 Repository 的固定 commit 交給真正的索引流程建進暫存資料庫，照問答的檢索規則取前 10 筆（問題對不上任何程式碼字詞時，例如中文，就用模型給的英文詞再搜一次；這些詞已凍結在題目裡，不呼叫 LLM），計算 recall@10 與 MRR，低於 `test/eval/cases.go` 記錄的下限就失敗。評測不接 Embedding，只量全文與符號檢索。deepcode 自己的 30 題跟著 `go test ./...` 跑；淺層 clone 沒有那個 commit 時會跳過，CI 要強制下限就得完整 fetch。其他七個 Repository 需要先在同一個資料夾準備 bare clone，名稱是題目的名稱加 `.git`（`chi.git`、`zustand.git`、`express.git`、`eshop.git`、`petclinic.git`、`gson.git`、`axum.git`），例如：

```bash
git clone --bare https://github.com/go-chi/chi chi.git
```

```bash
DEEPCODE_EVAL_GIT=/path/to/folder-with-the-clones go test -v -run TestRetrieval ./test/eval/
```

其餘的網址：https://github.com/pmndrs/zustand 、https://github.com/expressjs/express 、https://github.com/dotnet/eShop 、https://github.com/spring-projects/spring-petclinic 、https://github.com/google/gson 、https://github.com/tokio-rs/axum 。

| Repository | 語言 | 題數 | recall@10 | MRR |
| --- | --- | --- | --- | --- |
| chi | Go | 31 | 1.000 | 0.786 |
| zustand | TypeScript | 26 | 0.962 | 0.866 |
| express | JavaScript | 27 | 1.000 | 0.963 |
| eShop | C# | 30 | 0.800 | 0.612 |
| spring-petclinic | Java | 28 | 0.946 | 0.725 |
| gson | Java | 28 | 0.821 | 0.594 |
| axum | Rust | 30 | 0.800 | 0.467 |
| deepcode | Go、TypeScript | 30 | 0.917 | 0.620 |

調整檢索（欄位權重、融合、import 補位）前後都跑一次：分數上升就把下限調高；任一 Repository 下降時，只有整體明顯上升、且下降的題目經過檢查仍算合理才合併。bm25 的欄位權重（path、Symbol 名稱、內容、拆開的識別字）與測試文件的倍數掃過 162 組，採用 path 1、Symbol 名稱 6、內容 1、拆開的識別字 0.5，測試、文件與產生的檔案降為四分之一：八個 Repository 平均 recall 0.890→0.906、MRR 0.684→0.702，七個上升；gson 的「collections 與 arrays 怎麼轉成 JSON array」掉出 `CollectionTypeAdapterFactory.java`（只靠識別字裡的 collection 命中，第 6 名變第 11 名，換成同樣相關的 `JsonArray.java`）。問題裡相鄰的兩、三個字也連成一個名稱查 Symbol（route pattern 找到 `RoutePattern`），且只比對方法與函式，因為比對到型別時 eShop 的 catalog item、integration event 會把 `CatalogItem`、`IntegrationEvent` 類別排到最前面；chi 與 deepcode 的 MRR 上升，gson 的「accept alternate names」改為先找到同樣相關的 `FieldNamingStrategy.alternateNames()`，`SerializedName.java` 從第 1 名變第 2 名。想要的檔案寫成 `檔案#名稱` 時，要找到含那個 Symbol 的 Chunk 才算（chi 的 matched route pattern 一題用它，因為同一個檔案的其他 Chunk 本來就排得進前 10）。其他已知的方向：bm25 的欄位權重全部改成 1 時 chi、zustand、deepcode 都上升；Symbol 一律區分大小寫時 chi、eShop、gson、petclinic、deepcode 上升，但 zustand 的 MRR 與 axum 的 recall 下降（問題裡小寫的 path 找不到 `Path` extractor）。

### 基準量測（bench）

`test/bench` 對七個 DeepWiki 與 Code Wiki 都有 Wiki 的 Repository（chi、zustand、express、eShop、spring-petclinic、gson、axum）端到端量測，與它們比較用。每個 Repository 從 GitHub 抓固定的 base Commit 建索引、生成 Wiki，再更新到下一個 Commit（head）重建索引與 Wiki，最後在 head 上跑檢索評測並問 5 個固定問題。只有明確要求時才跑，會呼叫模型、從 GitHub 下載：

```bash
DEEPCODE_BENCH=1 DEEPCODE_CHAT_BASE_URL=http://host:8080/v1 DEEPCODE_CHAT_API_KEY=your-key DEEPCODE_CHAT_MODEL=your-chat-model go test -run TestBench -timeout 0 -v ./test/bench/
```

`DEEPCODE_BENCH_REPOS=chi,axum` 只跑其中幾個；`DEEPCODE_BENCH_PARALLEL=1,2` 讓第一次 Wiki 依序以每次 1 頁、2 頁並行各寫一次（每次從頭寫，保留最後一次；沒設時用 `DEEPCODE_WIKI_PARALLEL`，再沒有就是 1），`DEEPCODE_WIKI_MAX_PAGES` 照樣生效；沒設 `DEEPCODE_CHAT_MODEL` 時只量索引、關係圖與檢索評測；`DEEPCODE_REASONING_EFFORT` 與 `DEEPCODE_TEMPERATURE` 照伺服器的設定傳給模型，並記在結果裡。結果寫到 `data/bench/`：`latest.json`（全部數字與回答全文）、`latest.md`（摘要表與回答）、`runs/<開始時間>.json`（每次的紀錄）；工作資料在 `data/bench/work/`，每次重來。記錄的項目：檔案、產生的檔案、語言、Chunk、Symbol、import 關係的邊數與概覽關係圖畫出的箭頭數；第一次索引的時間；第一次 Wiki 的時間、頁數、模型呼叫次數、輸入與輸出的 token（伺服器有回報 usage 時，否則看字元數）與輸入中由提示詞快取讀取的 token 與比例；更新一個 Commit 的變更檔案數、索引與 Wiki 的時間、重寫的頁數（head 中與 base 同標題、去掉引用的行號與連結後文字相同的頁面不算：引用只是跟著行移動）；DeepWiki 主題覆蓋率（每個 Repository 列出 DeepWiki 那份 Wiki 的主題，詞彙表與治理文件除外，每個主題給幾個關鍵字，head 的頁面標題或 `##`、`###` 標題含其中之一（整個字、不分大小寫；名稱裡的字也算：小寫字母或數字後的大寫字母、連續大寫字母中後接小寫字母的最後一個是字的開頭，字母之後的數字之前、連續大寫字母之後而後面接著非字母數字、大寫字母或結尾的 s 之前是字的分界，`WelcomeController` 含 welcome、`HTTPServer` 含 server、`OAuth2` 含 oauth、`APIs` 與 `APIsController` 含 api，`Base64encode` 不含 encode；非 ASCII 的字元也是分界；`####` 與程式碼區塊裡的 `##` 不算，程式碼區塊從縮排至多三格、三個以上的 ``` 或 ~~~ 開始，到只有同一種字元、數量不少於開頭的那一行結束）就算涵蓋；表中列出沒涵蓋的主題）與只引用測試、CI、manifest、編輯器設定的頁數及比例；recall@10 與 MRR；每個問題的第一個字時間、總時間、引用數、必須出現的關鍵字是否都有（不分大小寫，取程式碼裡的名稱而非任何回答都會用的字，例如 chi 的 `RoutePattern`、eShop 的 `OrderPaymentSucceededIntegrationEvent` 與 `SetPaidOrderStatusCommand`；同一名稱有多種寫法時以 `a|b` 列出，出現其一即可；缺的列在表中）、是否自言自語與是否以問題的語言回答（去掉程式碼區塊、行內程式碼與連結後，漢字不少於英文單字算中文），以及通過關鍵字、沒有引用、自言自語、以其他語言回答的題數；第一次 Wiki 每種並行數的時間；head 的 Wiki 頁面樹（節與其下的頁面）。時間包含模型端點的延遲，比較時要分開看；第一次索引的時間包含從 GitHub 下載。某個步驟失敗時錯誤列在表格下方、索引表的「Errors」欄計數，它沒量到的格子一律標成 failed（第一次索引、head 的各項計數、失敗的 Wiki、更新與問題，以及任何一個查詢失敗的計數；沒算的重寫頁數是「–」；檢索評測失敗時該列不出現），不會當成正常數字；`DEEPCODE_BENCH_REPOS` 寫錯名字時直接失敗。bench 的檢索評測在更新過的版本上跑，Chunk 的編號順序和 `test/eval` 從頭建的不同，同分時的排名可能不同，分數會差千分之幾。

## 備份與復原

```bash
./deepcode backup --data ./data ./backup.db
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
- Repository 在 API、網址與畫面上只以 CUID2 識別，不用資料庫的流水號，從一個 id 推不出其他 Repository 的 id。id 不是秘密，權限照舊檢查：不存在的 id 與看不到的 Repository 一樣回 404。

## 目錄

每個資料夾最多 5 個檔案、5 個子資料夾，超過就依業務拆分；每個檔案只負責一件事，最多 500 行（npm 產生的 `web/package-lock.json` 除外）。

```text
cmd/deepcode/            cobra 指令：main（根指令、--data、開啟資料庫）、serve、backup、passwd
  config/                以 Viper 讀取設定：預設值、設定檔、環境變數，並檢查每個值
internal/
  app/                   組裝根：Config、元件建構、第一位管理員、事件驅動的背景流程與定期輪詢
    importer/            匯入：Repository 加入目錄，並在同一個 transaction 加進匯入者的角色
      store/             目錄新增 Repository 的 transaction 裡呼叫 role 的 store，加進匯入者的角色
    server/              HTTP 交付層：router 組合以下各組路由
      httpx/             共用：錯誤對應、JSON 綁定、Host / Content-Type 防護、session 與權限檢查、SSE（ping、重新驗證 session）
      authapi/           登入、註冊、登出、目前使用者、變更密碼
      repoapi/           Repository、程式碼、搜尋、Wiki、問答（handler 只解析請求、檢查權限、呼叫領域套件）
      adminapi/          後台：使用者、角色、Git 伺服器、工作與即時輸出
      webui/             內嵌的 React build（SPA fallback）
  access/                帳號與權限；各套件只放規則與流程，SQL 都在自己的 store/（持久化層）
    account/             使用者、密碼雜湊、第一位管理員、保留最後一位 admin
      store/             使用者與其角色、密碼雜湊的讀寫；停用、換密碼時在同一個 transaction 結束 session
    role/                權限清單、角色與其 Repository 範圍、使用者的權限判斷（Grants，依各角色範圍）
      store/             角色、權限與 Repository 範圍的讀寫、使用者各角色的授權、內建角色的 id
    session/             登入、註冊（開關與次數限制）、變更密碼、session、Principal
      store/             session（只存 token 的 SHA-256）與註冊開關的讀寫
  platform/              共用基礎設施
    storage/             SQLite 開啟、migrations（之後的依業務放子資料夾，依檔名編號排序）、備份、IN 查詢的佔位符、SQL 函式 cuid()
      cuid/              產生 CUID2：Repository 的公開識別碼
    cache/               記憶體 LRU Cache（含 singleflight 載入）
    events/              Watermill 事件匯流排、主題，以及給 SSE 用的短期監聽
    jobs/                工作佇列：worker 名額與掃描器、重試與退避、進度與輸出（job_logs）及其追蹤、過期工作清理
      store/             jobs 與 job_logs 的讀寫：依 idempotency key 排入、領取、記錄結果、清理
    ai/                  以官方 openai-go SDK 呼叫 OpenAI 相容端點：Chat Completions 或 Responses API 的串流回答、Embedding；不讀 OPENAI_* 環境變數，重試、RPM 節流、回應大小上限與錯誤中的金鑰遮蔽
  repo/                  Repository 的來源與索引；各套件只放規則與流程，SQL 都在自己的 store/（vectorstore 本身就是）
    gitserver/           Git 伺服器設定、OAuth 授權與 Token 換新、網址解析、來源連結格式；secret/ Token 加密與金鑰檔，oauth/ OAuth 同意頁網址、Token 端點與進行中的授權
      store/             git_servers 的讀寫（Token 存密文）、Repository 的來源伺服器
    provider/            GitHub / GitLab REST API：匯入資訊、連線測試
    git/                 git 指令（帶 Token 標頭的 fetch、ls-tree、cat-file）
    catalog/             Repository 目錄：匯入、同步與輪詢判斷、目前版本、檔案清單（快取）、內容與兩個版本間變更的檔案
      store/             Repository、目前版本、檔案清單與內容的讀寫；新增 Repository 的 transaction
    index/               索引
      analyzer/          語言判斷、產生的檔案、測試、範例、文件與隱藏資料夾的路徑；Go、JavaScript、TypeScript / TSX 的 Symbol 與 import，C#、Java、Rust 在下列子資料夾
        syntax/          各語言共用：Symbol、Grammar、附上前置註解的走訪、facts 的上限
        dotnet/          C#：Symbol 與 facts（namespace、using、宣告與用到的型別）
        jvm/             Java：Symbol 與 facts（package、import、宣告與用到的型別）
        rust/            Rust：Symbol（impl 的方法掛在型別上）與 facts（mod、use 與程式中的路徑）
      chunker/           Symbol-aware chunking；camelCase 識別字拆字（FTS5 的 terms 欄）
      graph/             import 解析成檔案之間的關係（Go 經 go.mod、JS/TS 經相對路徑，其他語言交給以下各資料夾）、被 import 的檔案、宣告變動的影響、目錄關係與 Mermaid 圖
        dotnet/          C#：namespace 與 using 找型別、global using、專案的 ProjectReference
        jvm/             Java：package 與 import 找型別
        rust/            Rust：Cargo.toml 的 crate 與 workspace、模組樹
        store/           檔案內容的 import 與 file_edges 的讀寫（分批寫入，服務中的版本一次換掉）、宣告與路徑
      indexer/           增量索引、Embedding、版本發佈與清理、補齊舊 Chunk 的 terms
        store/           版本的建立與發佈、檔案、Symbol 與 Chunk 的批次寫入、沿用向量、terms、清理
      vectorstore/       向量 BLOB 儲存與 Cosine 檢索
  knowledge/             由索引產生知識；各套件只放規則與流程，SQL 都在自己的 store/
    retrieval/           FTS5、Symbol、向量檢索、查詢點名的檔案與 RRF，再補上最佳結果 import 的程式碼
      store/             FTS5（BM25 權重）、定義 Symbol 的 Chunk、Symbol 搜尋、使用名稱的程式碼、依 id 讀 Chunk
    evidence/            Evidence Pack 與引用驗證
    chat/                問答與對話、Deep 模式（多輪檢索與背景 Wiki 頁）
      store/             對話與訊息（含引用）的讀寫、歷史訊息的分段、已定義的名稱、背景用的 Wiki 頁
    wiki/                Wiki 規劃、沿用上一版未受影響的頁面、並行撰寫與讀取；outline/ 頁數、分節與規劃的解析，steering/ 讀 .devin/wiki.json，change/ 逐行比對變更、算出沿用的引用移到哪裡與新檔案所屬的分組（含 manifest 的規則），fanout/ 限量並行
      store/             頁面、引用與語言的讀寫（規劃與寫完的頁各在一個 transaction）、Wiki 狀態、沿用的基準版本、要顯示的版本、規劃讀的檔案與宣告
test/                    e2e、檢索評測（eval）、基準量測（bench）、資料夾與檔案長度規則、依賴方向檢查
web/.config/             biome.jsonc：Biome 的格式與 lint 設定
web/public/              theme.js：第一次繪製前套用主題（CSP 不執行 inline 腳本）
web/src/                 React + TypeScript + Vite；Tailwind CSS v4 + shadcn/ui（Radix），動畫用 motion，路由用 TanStack Router（檔案路由，見 pages/routes/）；index.css 放主題 token（紙與墨兩套配色，訊號橘標示連結、引用與進行中）；字型隨程式打包（Fontsource 套件：Newsreader、Schibsted Grotesk、IBM Plex Mono），不向外部請求
  types/                 API 資料型別（不含執行期程式碼），分 admin/ 與 repo/
  lib/                   與業務無關的工具：useApi、useDialog、日期格式、URL 路徑編碼、查詢參數（路由的 validateSearch）、id 檢查與 Wiki 標題錨點
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
  pages/                 路由頁面，各自取資料、管狀態；多頁共用的 Frame、SiteHeader、useRepo、NotFoundPage 與 ErrorPage（路由出錯時的畫面）放在 pages/
    routes/              TanStack Router 的檔案路由，把網址的參數交給頁面：__root（等 session、換頁時淡入、404 與錯誤畫面）、/、(session)/ 的 /login 與 /signup（?next=）、repos/ 的 /repos/$id（?page=，或 ?file=&L=&s=）與 /repos/$id/search/{-$conversationId}、/admin/{-$section}/{-$id}；不合格的 Repository 或對話 id 以 notFound() 顯示 404。Vite 外掛由這些檔案產生 web/src/routeTree.gen.ts：納入版本控制（tsc 在 vite build 之前就要讀它），Biome 不檢查
    home/                Repository 列表、搜尋與加入
    repo/                Repository 頁：Wiki（wiki/）、檔案與 Symbol（files/）、同步
      search/            問答：串流回答與引用的檔案
    session/             登入與註冊頁、session（未登入時導向登入）
    admin/               後台：工作（即時列表、詳細與輸出）、使用者（含註冊開關）、角色、Git 伺服器
```

後端依賴方向：`platform` ← `repo` ← `knowledge`，`access` 不依賴其他區域；以上 ← `app/importer` ← `app/server` ← `app` ← `cmd`，不可反向；`cmd` 另直接使用 `app/server/webui`（內嵌的 UI）、`platform/storage`（開啟資料庫、`backup`）與 `access/account` 及其 `store`（`passwd`）。`access` 與 `repo`、`knowledge` 互不引用，匯入時由 `app/importer` 串接。前端：`types` 與 `lib` 是底層（`types` 不引用其他層，`lib` 只引用 `types`）；`api` 只引用 `types`、`lib`；`components` 只引用 `types`、`lib` 與其他 `components`（不呼叫 API、不引用 `pages`）；`pages` 組合以上各層。套件不受此限（例如 `components` 的連結用 `@tanstack/react-router` 的 `Link`，目的地照樣檢查型別）。

## 與計畫的差異

- **HTTP 框架**改用 Gin（計畫原為 `net/http`）。SSE 以手寫 `event:` / `data:` 框架輸出，維持前端解析的格式。
- **背景工作**以 Watermill GoChannel 事件驅動，SQLite `jobs` 表仍是唯一的真實狀態；事件只負責喚醒，遺失由掃描器補發。
- **增量同步**以 `git ls-tree` 的 blob SHA 比對檔案，而非 `git diff --name-status`：結果相同，也不需要舊 Commit 留在本地。
- **取得原始碼**以 shallow bare fetch 抓固定 SHA，再用 `git cat-file --batch` 讀取，不建立工作目錄。快取只保留正在索引的 Commit：換成別的 Commit 時整份重抓（depth 1 本來就會下載整棵樹，增量比對靠資料庫裡的 blob SHA），同步再多次也不會累積舊版本。下載時每 0.25 秒量一次快取大小、結束時再量一次，超過 `DEEPCODE_MAX_REPO_MB` 就中止並刪掉這份快取；抓取時關掉 Git 的自動維護，免得重新打包被算成下載量。每個 Git 指令的輸出最多保留 64 MB：上百萬個空檔案的樹只要下載幾 MB，`ls-tree` 卻要列出上百 MB，超過就讓該次索引失敗。
- **工作恢復**：資料庫只由一個程序使用，啟動時把 `running` 的工作重新排入（已用完重試次數的改為 `failed`），沒有 lease；多程序共用資料庫時才需要。
- **向量儲存**（`vectorstore.SQLite`）的 `Upsert` 一次寫多筆；沒有 `Delete`，向量隨 Chunk 以外鍵 cascade 刪除。沒有熱門向量的記憶體快取，量測到掃描成為瓶頸再加。
- **migrations** 放在 `internal/platform/storage/migrations/` 並編入執行檔；`002_fts_porter.sql` 把 FTS5 改為 porter 詞幹，並在同一個 transaction 內重建既有資料的索引。`003_access.sql`（帳號、角色、session）與 `004_git_servers_job_logs.sql`（Git 伺服器、工作輸出）讓既有資料庫升級：原有 Repository 歸到 GitHub 伺服器，原有對話歸給第一位管理員。`005_signup.sql` 新增註冊開關（預設關閉）。之後的 migration 依業務放進子資料夾（每個資料夾最多 5 個檔案），例如 `knowledge/006_wiki_language.sql`：仍記錄為 `migrations/<檔名>`、依檔名的編號排序，所以檔案搬進子資料夾不會重跑；兩個同名檔案會讓啟動失敗。`006_wiki_language.sql` 記錄每個版本的 Wiki 以哪種語言撰寫：新版本只沿用同語言 Wiki 的頁面，續寫時語言不同就重新規劃（之前的 Wiki 沒有記錄語言，不會被沿用，未完成的會重新規劃）。`repo/007_chunk_terms.sql` 為 Chunk 加上 camelCase 識別字拆開後的單字欄（`terms`），並以它重建 FTS5 索引；SQL 拆不了 camelCase，所以升級前的 Chunk 在升級後第一次啟動時於背景分批補上（中斷時下次啟動接續，補完後 log 寫出筆數），補完之前照舊只以原文比對。`repo/008_import_graph.sql` 在檔案內容旁存放它 import 的模組（`file_contents.imports`），並新增 `file_edges`（每個版本 import 解析後的檔案關係）；索引時讀取新檔案的 import，升級前的檔案在下次連結該版本時從資料庫裡的內容讀取，已發佈的版本在升級後第一次啟動時於背景連結（log 寫出版本數）。`repo/009_generated_files.sql` 在 `source_files` 加上 `generated`（產生的檔案），並在 `snapshots` 記錄建索引的分析器版本（`analyzer`）；升級前的檔案一律當作手寫的，該 Repository 下次索引時不沿用舊版本的檔案資料，全部重新分析，C#、Java、Rust 檔案才有 Symbol，產生的檔案才標出來。HEAD 沒變的 Repository 也一樣：同步（手動或自動輪詢）看到已發佈的版本是舊分析器讀的，就排一次索引，就地逐檔重新分析（期間照常服務：每個檔案在一個 transaction 內換新，內容沒變的 Chunk 沿用原本的向量，import 關係最後一次換掉；Wiki 不重寫），不再索引的檔案移除；舊版本開始、中途停下的索引被新版本接手時清空重來。檔案的 import 以新分析結果為準。之後分析方式改變時調高 `analyzer.Version` 即可。`repo/010_repository_public_id.sql` 為 Repository 加上公開識別碼 `public_id`：升級時為既有的 Repository 補上，之後新增的由 trigger 產生，所以舊網址（`/repos/1`）升級後失效，要從首頁重新進入。採用 CUID2 而非原版 CUID：原版含時間戳與計數器、可被推測，已被作者棄用；CUID2 把時間、亂數、計數器與行程指紋以 SHA3-512 雜湊，不需要新的套件（標準函式庫的 `crypto/sha3`）。值由執行檔註冊的 SQL 函式 `cuid()` 產生，migration 的補值與 trigger 共用；其他 SQLite 工具沒有這個函式，無法新增 Repository。資料表之間的關聯與權限判斷仍用整數 id：外鍵、索引與 join 都不必改，只在 API 的邊界換成公開識別碼。`knowledge/011_wiki_parent.sql` 為 Wiki 頁面加上 `parent`（所屬節的頁面在同一版本 Wiki 中的位置，最上層為 NULL）：升級前的 Wiki 都是單層；Repository 的大小要分節時，下一個版本會重新規劃成兩層。`access/012_role_descriptions.sql` 把內建角色 admin 與 member 的說明改成繁體中文；管理員改寫過的說明不變。
- **Wiki 增量更新**以頁面的引用、重新檢索的結果，以及 import 了宣告有變的檔案判斷受影響的頁面；import 關係只到檔案層級，沒有呼叫圖。寧可多重寫，也不留下與程式碼不符的頁面。
- **import 解析**：Go 經 go.mod 模組路徑、JS/TS 經相對路徑（含省略副檔名、`index.*`、`.js` 對應 `.ts`），C#、Java、Rust 見「語言」；套件名稱、tsconfig 的 `paths`（如 `@/components`）、go.work、Rust 的 `#[path]` 與改名的依賴、C# 的 namespace 別名不解析。C# 與 Java 以型別名稱找檔案，不做型別推論：同名的型別、泛型參數與大寫開頭的屬性可能多連或少連，所以每個檔案最多 50 條。
- **帳號與 Git 伺服器**是計畫之外新增的：計畫只匯入公開 GitHub、沒有登入機制。
- 沒有 Makefile。問答的 Deep 模式只多做最多 2 輪由模型決定字詞的檢索，不呼叫工具，也不是 DeepWiki 的 Deep Research Agent。
