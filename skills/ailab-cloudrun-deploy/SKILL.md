---
name: ailab-cloudrun-deploy
description: >
  部署網頁或後端服務到 Google Cloud Run。適合非工程師使用，全自動引導。
  Use when: 使用者說「部署到 Cloud Run」、「deploy to Cloud Run」、「上版到 Cloud Run」等。
---

# Cloud Run 部署技能

本技能引導使用者將網頁應用程式或後端 API 部署到 Google Cloud Run，全程自動化，不需要 Docker 或 GCP 知識。

## 預設值

| 項目 | 預設值 |
|------|--------|
| GCP 專案 | `ailab-494105` |
| 區域 | `asia-east1` |
| 服務名稱 (測試) | `{資料夾名}-dev` |
| 服務名稱 (正式) | `{資料夾名}-prod` |
| 認證方式 | Google Group（使用者個人帳號） |
| VPC egress 出口 IP | dev / prod 各一條固定 IP（由 Cloud NAT 自動配發） |
| 對外 URL（sub-path）| `https://ailab-{env}.cmoney.tw/{path}/`（`{path}` 預設 `{APP}`，可在 Step 2-3.5 自訂；部署完 skill 自動接 LB，不需找 platform admin） |
| URL Router Helper | `https://ailab-{env}.cmoney.tw/url-router`（內部 service，dev/prod 各自獨立） |
| Swagger / API 文件路徑 | 因框架而異：.NET / NestJS = `/swagger`；FastAPI = `/docs`；Flask-RESTX = `/`；其他請依框架預設 |

> **維運備註（給 platform admin 看）**：上方 helper URL 是 SoT，綁在 LB 子路徑、不會因重新部署變動。每個 env 用獨立 SM key（`url-router-key-{env}`）。若 URL 變動，改本表格 + Step 4.5-1 與 Step 5 Case A2 的 `HELPER_URL=...`，然後跑 `cm-ailab-mp:update` 重發 skill。

---

## Step 1: 前置檢查

在開始部署前，依序執行以下檢查。任何一項失敗就中斷流程。

### 1-1. 檢查 gcloud CLI

```bash
which gcloud
```

- 如果找不到 `gcloud`，自動安裝：
  - macOS：
    ```bash
    brew install --cask google-cloud-sdk
    ```
  - 如果沒有 brew，使用官方安裝腳本：
    ```bash
    curl https://sdk.cloud.google.com | bash
    source ~/.zshrc 2>/dev/null || source ~/.bash_profile 2>/dev/null || source ~/.bashrc 2>/dev/null || echo "請開新 terminal 視窗讓 PATH 生效"
    ```
  - 安裝完成後重新執行 `which gcloud` 確認成功。
  - 如果安裝失敗，告知使用者請找工程師協助。

### 1-2. 檢查 Google 帳號登入狀態

```bash
gcloud auth list --filter="status:ACTIVE" --format="value(account)"
```

- 如果沒有 active account，引導使用者登入：
  > 你還沒有登入 Google 帳號。請執行以下指令登入：
  > ```
  > gcloud auth login
  > ```
  > 瀏覽器會打開 Google 登入頁面，用你的公司帳號登入即可。

（後續所有指令會明確帶 `--project=ailab-494105`，不修改你的全域 gcloud 設定。）

### 1-3. 檢查部署權限

跑三個 IAM 檢查：

```bash
# Cloud Run admin 權限
gcloud run services list --project=ailab-494105 --region=asia-east1 --limit=1 2>&1
# Cloud Build editor 權限（--source 部署用 Cloud Build）
gcloud builds list --project=ailab-494105 --region=asia-east1 --limit=1 2>&1
# Secret Manager 讀取權限（Step 4.5 自動接 URL 會用到）— dev 和 prod key 各檢一次
gcloud secrets versions access latest --secret=url-router-key-dev --project=ailab-494105 >/dev/null 2>&1
gcloud secrets versions access latest --secret=url-router-key-prod --project=ailab-494105 >/dev/null 2>&1
```

Cloud Run 或 Cloud Build 任一出現 `PERMISSION_DENIED`，中斷流程：

> 你的帳號目前沒有 [Cloud Run / Cloud Build] 部署權限。請聯繫 kevin_kuo@cmoney.com.tw 開通。

如果 Secret Manager 拿不到（PM 沒在 `ai_lab@cmoney.com.tw` group 或 secret 不存在），**中斷部署**：
> ❌ 你拿不到 url-router 的 SM key（沒在 `ai_lab@cmoney.com.tw` group 或 secret 暫時不可讀）。
>
> 本技能部署的服務一律對外（會綁到 ailab LB），少了這把 key 沒辦法自動接 URL，部署沒有意義。請先處理權限再重跑：
>   1. 請聯繫 kevin_kuo@cmoney.com.tw 把你加進 `ai_lab@cmoney.com.tw` group。
>   2. 確認 group 加好後，重新登入 gcloud (`gcloud auth login`) 讓憑證刷新。
>   3. 重跑 `/ailab-cloudrun-deploy`。

---

## Step 2: 收集部署資訊

### 2-1. 部署來源

使用當前工作目錄（`pwd`）作為部署來源，服務名稱從當前資料夾名稱自動產生。

**資料夾名稱規則：** 服務名稱會自動套用以下轉換以符合 Cloud Run 規則（小寫英文開頭，只含小寫英文/數字/連字號，最長 49 字元）：
1. 全部轉小寫
2. `_` 替換為 `-`
3. 開頭非英文字母（例如 `2fa`）→ 中斷流程，請 PM 自訂服務名稱
4. 含無法替換的特殊字元（如 `.`、空格、中文）→ 中斷流程，請 PM 改資料夾名或自訂服務名稱

範例：
- `MyApp` → `myapp`
- `tax_better` → `tax-better`
- `2fa-service` → ❌ 中斷，請手動指定例如 `two-fa-service`

### 2-2. 自動偵測專案類型

進入原始碼目錄，依照以下規則判定專案類型：

**判定為「網頁」的條件（符合任一）：**
- 根目錄有 `index.html`
- `package.json` 存在且含有 `build` script，但不存在 API 入口檔案（如 `server.js`、`server.ts`、`app.py`、`main.go`）

**判定為「後端 API」的條件（符合任一）：**
- 存在 `swagger.json`、`swagger.yaml`、`openapi.json`、`openapi.yaml` 等設定檔
- 存在 `main.go`、`app.py`、`server.ts`、`server.js` 等 API 入口檔案
- 存在 `*.csproj` 或 `*.sln` 檔案（.NET 專案）

**自動判定結果一律跟使用者確認**（避免偵測錯誤導致後續 basePath 驗證或 swagger 路徑帶錯訊號）：

- 自動判定為「網頁」：
  > 我偵測到這是**網頁**專案（依據：例如 `index.html` / `next.config.ts` / `package.json` 含 build script）。確認是嗎？
  > 1. 對，是網頁
  > 2. 不對，這是後端 API
- 自動判定為「後端 API」：
  > 我偵測到這是**後端 API** 專案（依據：例如 `main.go` / `app.py` / `swagger.yaml` / `*.csproj`）。確認是嗎？
  > 1. 對，是後端 API
  > 2. 不對，這是網頁
- 無法判定：
  > 無法自動判斷這是網頁還是後端 API。請問這是：
  > 1. 網頁（前端頁面）
  > 2. 後端 API（提供資料的伺服器）

PM 的最終答案才是定案，蓋過自動偵測結果。把偵測依據（哪幾個檔案命中了規則）一併秀給 PM 看，方便他驗證。

### 2-3. 部署環境

詢問使用者要部署到哪個環境：

> 要部署到哪個環境？
> 1. 測試機 (dev) — 用於內部測試
> 2. 正式機 (prod) — 正式上線環境

- 測試機 → 服務名稱後綴 `-dev`，**一律走 sub-path** `https://ailab-dev.cmoney.tw/{path}/`（測試機不開 subdomain）
- 正式機 → 服務名稱後綴 `-prod`，需要再問一次想走哪種對外網址模式

如果使用者選 **正式機 (prod)**，再問：

> 正式機要走哪一種對外網址？
> 1. sub-path（預設、不需額外申請）—— `https://ailab-prod.cmoney.tw/{path}/`
> 2. subdomain（例如 `tax-saving.cmoney.tw`）—— **需要聯繫 kevin_kuo@cmoney.com.tw 一次性 setup**（DNS / cert / LB 由他處理）；不是部署當下做的事，請先確認他已處理或同步發起申請

> ⚠️ 選 subdomain 但 kevin_kuo@cmoney.com.tw 還沒處理完 → 部署完之後 subdomain 不會通，要等設定完成才能對外。在等的期間可以先用 Cloud Run 直連 URL（部署完後 skill 會給你）做內部測試。

如果使用者選 **subdomain**，再問：

> 你的子網域 prefix 是什麼？預設：{APP}
> （絕大多數用預設即可。只有品牌改名才需要改，例如資料夾叫 `tax-better` 但對外想叫 `tax-saving` → `tax-saving.cmoney.tw`）

PM 按 Enter 用預設（`{product} = {APP}`），或輸入自訂值。把答案綁成 `{product}` 變數。
prefix 必須符合 DNS 規則：小寫英文/數字/連字號，不可開頭或結尾用連字號，最長 63 字元。
偵測到不合規 → 重新問。

兩個模式對 code 的要求剛好相反：

| 模式 | 對外 URL bar 顯示 | code 必須的 basePath |
|---|---|---|
| dev（永遠）| `ailab-dev.cmoney.tw/{path}/...` | `'/{path}'` |
| prod sub-path | `ailab-prod.cmoney.tw/{path}/...` | `'/{path}'` |
| prod subdomain | `{product}.cmoney.tw/...` | **不設**（或空字串） |

兩者皆使用 `--allow-unauthenticated`（公開存取），認證由應用程式自行處理。

### 2-3.5. 對外路徑 (path) — **僅 sub-path 模式**

**前置條件**：只在以下情境執行（subdomain 模式跳過本步、直接到 Step 2-4）：
- env=dev（永遠 sub-path）
- env=prod 且模式=sub-path

詢問使用者：

> 對外路徑要叫什麼？預設：`{APP}`
> （絕大多數用預設即可。只有品牌改名才需要改，例如資料夾叫 `tax-better` 但對外想叫 `tax-saving` → `https://ailab-{env}.cmoney.tw/tax-saving/`）

PM 按 Enter 用預設（`{path} = {APP}`），或輸入自訂值。

**驗證 regex** `^[a-z][a-z0-9-]*(/[a-z][a-z0-9-]*)*$`：路徑只能用小寫英文、數字、連字號（可含 `/` 做多層 path，例如 `team/tax-saving`），不能有大寫、空格、開頭斜線或特殊字元。不過則重新問。

把答案綁成 `{path}` 變數（**不含**開頭斜線）。

### 2-4. 環境變數

dev 和 prod **一定要分開收**（DATABASE_URL / API key / API_BASE_URL 等同一個 key 在不同 env 值不同）。

依 Step 2-3 選的 `{env}` 決定要載入哪個 env 檔，**優先順序**：

| {env} = dev | {env} = prod |
|---|---|
| `.env.dev` → `.env.development` → `.env` → 無 | `.env.prod` → `.env.production` → `.env` → 無 |

掃描順序：

1. 依上表找出**第一個存在**的 env 檔，把它的 key/value 載入記憶體
2. 掃描程式碼中引用的環境變數（如 `process.env.XXX`、`os.Getenv("XXX")`、`os.environ["XXX"]`、`Environment.GetEnvironmentVariable("XXX")`）
3. 把 code 引用的 key 跟 env 檔的 key 合併，得到完整清單

向使用者展示時，**明確標出當前部署 env + 來源檔案**：

> 你正在部署到 **{env}**。我從 `{找到的檔名}` 載入了這些環境變數（沒找到專屬檔的話，標註 fallback 來源）：
> - DATABASE_URL = postgres://...（來自 .env.{env}）
> - API_KEY = ***（來自 .env.{env}）
> - REDIS_URL（程式碼引用但檔案沒給值 — 請補）
>
> 請確認這些值都是 **{env} 環境**該用的，或告訴我哪些要改 / 不需要。

完全找不到任何環境變數需求 → 跳過此步驟。

**⚠️ Fallback 警告**：如果是因為沒找到 `.env.{env}`、退而求其次用 `.env` 的情況，**逐項確認**那些值是否真的對應到 {env} 環境（很可能 `.env` 裡放的是 dev 值，部署到 prod 會錯）。常見要檢查的：

- `DATABASE_URL` 不能是 `localhost:5432` → 用 Cloud SQL / GKE pgbouncer endpoint
- `REDIS_URL` 同理
- `API_BASE_URL` 不能指向 `127.0.0.1`、私有 IP
- 任何 `*_HOST` / `*_URL` 是 dev-only 的服務名稱

如果 `.env` 裡混了 dev-only 值，**逐項問 PM** 真實的 {env} 環境值，不要照搬。

收集所有環境變數，後續組成 `--set-env-vars` 參數。

**特別提醒（前端框架）：** Cloud Run buildpacks 在 build 階段預設會設 `NODE_ENV=production`。
但如果你的 build script 在 runtime 才執行（例如 `npm start` 內含 `next build`），或自訂
Dockerfile 沒帶這個 env var，就會 ship 出 dev build（大、慢、含 debug 輸出）。檢查方法：
build 完看 console 是否出現 `Compiled in production mode` / `optimization` 字樣，沒有就要
手動 `--set-env-vars NODE_ENV=production`。

**注意：re-deploy 時 `--set-env-vars` 會取代所有現有環境變數**（不是 merge）。如果這個服務之前已經部署過、Cloud Run 上有舊環境變數，請先看一下：

```
gcloud run services describe {APP}-{env} --project=ailab-494105 --region=asia-east1 --format="value(spec.template.spec.containers[0].env)"
```

把舊變數一起列入這次的清單，避免漏設導致服務缺欄位。或改用 `--update-env-vars`（增量更新）。

### 2-4-extra: 含逗號或特殊字元的環境變數

如果某個環境變數值本身含有 `,` `=` 或多行內容（例如 `DATABASE_URL` 含多 host、JSON 字串），
標準 `--set-env-vars "K1=v1,K2=v2"` 會解析錯誤。改用以下兩種方式之一：

- **跳脫分隔符**（單一指令內）：
  ```
  --set-env-vars "^|^DATABASE_URL=postgres://h1,h2/db|API_KEY=abc"
  ```
  把 `^|^` 當分隔符（任意非衝突字元都行）。

- **env-vars-file**（推薦複雜情境）：建立 `.env.deploy` YAML：
  ```yaml
  DATABASE_URL: postgres://h1,h2/db
  API_KEY: abc
  ```
  然後 `gcloud run deploy ... --env-vars-file .env.deploy`，並把 `.env.deploy` 加到 `.gitignore`。

### 2-5. 路徑前綴 (basePath) 驗證

本步驟根據前面已收集的設定驗證 code 的 basePath 是否設正確（sub-path 模式看 Step 2-3 + 2-3.5 的 `{path}`；subdomain 模式只看 Step 2-3 的 `{product}`，2-3.5 不會跑）：

| 環境 + 模式 | 對外 URL | code 期望的 basePath |
|---|---|---|
| dev（一律 sub-path）| `https://ailab-dev.cmoney.tw/{path}/...` | `'/{path}'` |
| prod **sub-path** | `https://ailab-prod.cmoney.tw/{path}/...` | `'/{path}'` |
| prod **subdomain** | `https://{product}.cmoney.tw/...` | **空字串 / 不設** |

- `{path}` = Step 2-3.5 收集的對外路徑（預設 `{APP}`，可被 PM 自訂）。
- `{APP}` = 當前資料夾名（service name 去掉 `-dev`/`-prod` 後綴；本步驟內以下出現的 `{APP}` 只用在這個說明處，basePath 的實際比對都以 `{path}` 為準）。

**為什麼這個檢查重要：**
- **sub-path 模式**下，LB 把 path 原封轉給 Cloud Run（**不 rewrite**）。code 沒設 basePath → HTML 內的 `/_next/...`、`/icons/...` 等絕對路徑被當 root → LB 找不到 → **白屏**。API 同理：route 掛在 `/` 但呼叫端打 `/{path}/...` → 404。
- **subdomain 模式**下，subdomain 的流量**一樣經過同一個 ailab LB**（不是 DNS 直連 Cloud Run），URL map 多一條 hostRule 把它打到對應 backend，path 原樣 passthrough（不 rewrite）。如果 code 還留著 `basePath: '/{path}'` → 服務只認 `/{path}/page`，但 LB 傳過來的是 `/page`（subdomain 沒帶前綴）→ **整站 404**。

依照偵測到的框架，檢查對應設定檔的 basePath 欄位：

| 框架 | 偵測訊號 | 必須設定 |
|---|---|---|
| Next.js | `next.config.{js,ts,mjs,cjs}` 存在 | `basePath: '/{path}'`（強烈建議連 `assetPrefix: '/{path}'` 也設） |
| Nuxt 3 | `nuxt.config.{ts,js}` 存在且 deps 有 `nuxt` v3+ | `app.baseURL: '/{path}/'` |
| Nuxt 2 | `nuxt.config.{ts,js}` 存在且 deps 有 `nuxt` v2 | `router.base: '/{path}/'` |
| Vite (Vue/React/Solid) | `vite.config.{ts,js}` 存在 | `base: '/{path}/'` |
| Create React App | `package.json` 有 `react-scripts` | `"homepage": "/{path}"` 寫在 package.json |
| SvelteKit | `svelte.config.js` 存在 | `kit.paths.base: '/{path}'` |
| Angular | `angular.json` 存在 | `index.html` 內有 `<base href="/{path}/">` 或 build flag `--base-href /{path}/` |
| 純靜態 HTML | 只有 `index.html`，無 build 工具 | 所有 `src` / `href` 用**相對路徑**（不能 `/xxx` 開頭）；或在 `<head>` 加 `<base href="/{path}/">` |
| FastAPI | `main.py` / `app.py` import `fastapi` | `FastAPI(root_path='/{path}')` 或部署參數 `--root-path /{path}` |
| Flask | import `flask` | 設 `APPLICATION_ROOT='/{path}'` 並用 `werkzeug.middleware.proxy_fix.ProxyFix` |
| Express | import `express` | 所有 router 掛在 prefix 下：`app.use('/{path}', router)` |
| ASP.NET Core | `Program.cs` 或 `Startup.cs` | `app.UsePathBase("/{path}")`（在 `app.UseRouting()` 之前） |
| Spring Boot | `application.{yaml,yml,properties}` 存在 | `server.servlet.context-path: /{path}` |
| Go (gin / chi / gorilla / std net/http) | `main.go` 引用對應 router | mux subrouter 加 prefix，或全部 handler 註冊在 `/{path}/...` 下 |

偵測到框架後，把對應的設定檔名稱（`next.config.ts` / `vite.config.ts` / `nuxt.config.ts` /
`svelte.config.js` / `angular.json` / `application.yaml` / `Program.cs` 等）綁成 `{configFile}`，
後續 FAIL 訊息會引用。

**為何各框架尾斜線不一樣？** 這是各框架對 basePath 的內部慣例，不是寫錯。請嚴格按表格填，
不要自己加減 `/`。例如 Next.js `basePath: '/{path}'`（無尾斜線）vs Vite `base: '/{path}/'`（有尾斜線），
是各自 routing 引擎的歷史 API，混用會出 404 / 重定向迴圈。

**驗證流程：**

1. 取得 `{path}` = Step 2-3.5 收集的對外路徑（預設 `{APP}` = 資料夾名）
2. 取得 Step 2-3 的 (env, mode) → 算出「期望的 basePath 值」：
   - dev / prod sub-path → 期望 `/{path}`
   - prod subdomain → 期望 `''`（空字串 / 不設）
3. 用框架表偵測專案類型
4. 讀對應 config，grep 對應欄位的值（可能是字面字串，也可能是 `process.env.XXX || ''` 之類的 build-time 動態值）
5. 比對：
   - ✅ **PASS**：實際值 = 期望值 → 顯示綠勾，繼續
     - 若實際值是 `process.env.NEXT_PUBLIC_BASE_PATH || ''` 之類的動態值，視為 PASS，但要在 deploy 階段確保 build env var 設對（sub-path 模式要 `--set-build-env-vars NEXT_PUBLIC_BASE_PATH=/{path}`，subdomain 模式則不傳）
   - ❌ **FAIL**：實際值 ≠ 期望值 → 中斷部署，顯示具體修法（見下面 Case A / Case B）
   - ⚠️ **WARN**：偵測不出框架類型 → 列出常見設定方式請使用者自己確認，問 `(Y/n)` 是否仍要繼續

**FAIL Case A — sub-path 模式，但 code 沒設 basePath（最常見）：**

```
========== ❌ basePath 驗證失敗 (sub-path 模式) ==========
你選擇的是 sub-path 模式，對外 URL 會是：
  https://ailab-{env}.cmoney.tw/{path}/

但 {configFile} 沒看到 basePath。HTML 內的 /_next/、/icons/ 等絕對
路徑會被 LB 當成 root 找不到 → 白屏。

請在 `{configFile}` 加上（以下以 Next.js 為例；其他框架請依 Step 2-5 表格的對應設定）：

  const nextConfig = {
    basePath: '/{path}',
    assetPrefix: '/{path}',
  }

改完 git commit 後再重新部署。
==========================================================
```

**FAIL Case B — prod subdomain 模式，但 code 還留 basePath：**

```
========== ❌ basePath 驗證失敗 (subdomain 模式) ==========
你選擇的是 prod subdomain 模式（{product}.cmoney.tw），但 `{configFile}`
仍然設了 basePath（值為 `/{path}` 或類似）。

subdomain 模式下服務直接掛在 root，code 不能有 basePath，否則整站
404（因為框架期望 /{path}/page 但實際收到 /page）。

最簡單修法（以下以 Next.js 為例；其他框架請改為對應的 basePath 欄位設為空）：

  const nextConfig = {
    basePath: '',           // 或整行刪掉
    assetPrefix: '',        // 或整行刪掉
  }

更建議的彈性做法（同一份 code 同時支援 dev sub-path + prod subdomain，
不用每次切模式都改 code，以下以 Next.js 為例）：

  const nextConfig = {
    basePath: process.env.NEXT_PUBLIC_BASE_PATH || '',
    assetPrefix: process.env.NEXT_PUBLIC_BASE_PATH || '',
  }

然後 deploy 時用 build env var 控制：
  - dev (sub-path):     --set-build-env-vars NEXT_PUBLIC_BASE_PATH=/{path}
  - prod (sub-path):    --set-build-env-vars NEXT_PUBLIC_BASE_PATH=/{path}
  - prod (subdomain):   不傳這個 env var
==========================================================
```

**其他框架的彈性做法（同樣概念，env var 控制 basePath）：**

| 框架 | 設定方式 |
|---|---|
| Vite | `base: process.env.VITE_BASE_PATH || '/'`，deploy 帶 `--set-build-env-vars VITE_BASE_PATH=/{path}/` |
| Nuxt 3 | `app.baseURL: process.env.NUXT_APP_BASE_URL || '/'`，deploy 帶 `--set-build-env-vars NUXT_APP_BASE_URL=/{path}/` |
| SvelteKit | `kit.paths.base: process.env.BASE_PATH || ''`（注意 SvelteKit basePath 不能有尾斜線） |
| Spring Boot | `server.servlet.context-path: ${BASE_PATH:/}`，runtime env var `BASE_PATH=/{path}` |

其他框架原則一樣：basePath 設成 `process.env.XXX || ''`，dev/sub-path 模式 deploy 時帶對應 env var。

**WARN 範例輸出：**

```
========== ⚠️ 無法自動驗證 basePath ==========
偵測不出你的網頁框架是哪一個。請自己確認 code 符合所選模式：

  - 你選擇的模式是 [dev / prod sub-path / prod subdomain]
  - sub-path 模式 → code 必須設 /{path} 前綴（例如 Next.js basePath、
    Vite base、Spring Boot context-path 等）
  - subdomain 模式 → code 不能設 basePath（要在 root serve）

如果設錯，部署後可能整站白屏 / 404。

要繼續部署嗎？(Y/n)
==============================================
```

> **註：** 本技能部署出來的服務一律對外（dev/prod 都會綁到 ailab LB 對外網址），沒有「純內部」分支。不需要詢問「這個服務會對外提供服務嗎」。

---

## Step 3: 確認摘要

在執行部署前，顯示摘要讓使用者確認（**對外網址直接顯示完整 URL，部署成功後就是這個網址**）：

```
========== 部署確認 ==========
服務名稱：  my-app-dev
專案類型：  網頁 / 後端 API
環境：      測試機 (dev) — 公開存取
                  或   正式機 (prod) — 公開存取
正式機模式：（只在 env=prod 時出現這行）
            sub-path  → 對外網址 https://ailab-prod.cmoney.tw/{path}/
            或 subdomain → 對外網址 https://{product}.cmoney.tw/（kevin_kuo@cmoney.com.tw 已備好子網域）
對外網址：  https://ailab-{env}.cmoney.tw/{path}/    ← 部署成功後 Step 4.5 會自動把服務綁到這裡
            （subdomain 模式時改顯示 https://{product}.cmoney.tw/）
路徑前綴：  /{path}  ✅  basePath 已驗證（sub-path 模式）
            或：  ✅  basePath 已驗證（subdomain 模式：code 沒設 basePath，符合期望）
            或：  ⚠️  無法自動驗證，使用者已確認自行負責
環境變數：  DATABASE_URL（已設定）
            API_KEY（已設定）
==============================

確認以上資訊無誤嗎？(Y/n)
```

**subdomain 模式範例：**
```
========== 部署確認 ==========
服務名稱：  my-app-prod
專案類型：  網頁 / 後端 API
環境：      正式機 (prod) — 公開存取
正式機模式： subdomain → 對外網址 https://my-product.cmoney.tw/
            ⚠️ DNS / cert / LB 設定由 kevin_kuo 一次性處理，未完成前用 Cloud Run 直連 URL 測試
對外網址：  https://my-product.cmoney.tw/
路徑前綴：  ✅ basePath 已驗證（subdomain 模式：code 沒設 basePath）
環境變數：  DATABASE_URL（已設定）
==============================
```

注意：環境變數只顯示 key 名稱，不顯示值（避免洩漏敏感資訊）。

使用者確認後才繼續。如果使用者要修改，回到對應步驟重新收集。

---

## Step 4: 執行部署

**重要安全原則：** 組合 `gcloud run deploy` 指令時，`--set-env-vars` / `--env-vars-file`
參數的具體 **值不要顯示在對話中**。直接執行指令、用 `***` 取代 value 印給 PM 看一次摘要。
失敗訊息也避免 echo 完整指令字串。

**確保你在專案根目錄才跑下面這個指令** — `--source .` 會打包當前目錄。如果不確定，先 `pwd` 確認。

根據環境和設定組合 `gcloud run deploy` 指令。

```bash
gcloud run deploy {APP}-{env} \
  --source . \
  --project ailab-494105 \
  --region asia-east1 \
  --allow-unauthenticated \
  --network=default \
  --subnet=ailab-asia-east1-{env} \
  --vpc-egress=all-traffic
```

`--network` / `--subnet` / `--vpc-egress` 三個 flag 把服務的對外流量導到 Cloud NAT 固定 IP，後端白名單服務才認得。

### 有環境變數時

加上 `--set-env-vars` 參數：

```bash
gcloud run deploy {APP}-{env} \
  --source . \
  --project ailab-494105 \
  --region asia-east1 \
  --allow-unauthenticated \
  --network=default \
  --subnet=ailab-asia-east1-{env} \
  --vpc-egress=all-traffic \
  --set-env-vars "KEY1=value1,KEY2=value2"
```

**部署成功後（不論走哪個指令 variant）必做：取得 `{cloudRunUrl}` 並綁定備用**

- gcloud 在最後會印 `Service URL: https://...-{hash}-de.a.run.app` → 抓 URL 部分
- 抓不到的話 fallback：
  ```
  gcloud run services describe {APP}-{env} --project=ailab-494105 --region=asia-east1 --format="value(status.url)"
  ```

把這個值綁成 `{cloudRunUrl}` 變數備用。

---

## Step 4.5: 自動接對外網址（**僅 sub-path 模式**）

**前置條件**：本步驟**只在以下情境執行**：
- env=dev（永遠 sub-path）
- env=prod 且模式=sub-path

> Step 1-3 已確認 SM key 拿得到（拿不到會在 Step 1-3 直接中斷部署），這裡不用再 fallback。

**跳過整個 Step 4.5、直接進 Step 5 的情境**：
- Step 2-3 選了 prod subdomain 模式 → Step 5 走 Case B

> **註：** Step 3 已整體確認過部署計畫（含對外網址），這裡直接執行綁定，不再單獨問 PM。`{path}` 在 Step 2-3.5 已收集並通過 regex 驗證。

### 4.5-1. 拿 SM key + 呼叫 helper

告訴 PM 接線進行中：

> 部署完成，正在請 url-router-helper 處理綁定到 https://ailab-{env}.cmoney.tw/{path}/（建 NEG / backend / 加 LB 規則 / 等 LB propagate / 驗 200）...
> 通常 1-6 分鐘。請勿關閉終端。

然後執行：

```bash
KEY=$(gcloud secrets versions access latest --secret=url-router-key-{env} --project=ailab-494105)
EMAIL=$(gcloud config get-value account 2>/dev/null)
HELPER_URL=https://ailab-{env}.cmoney.tw/url-router

curl -s --max-time 360 -X POST "$HELPER_URL/bind" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $KEY" \
  -H "X-User-Email: $EMAIL" \
  -d '{"app":"{APP}","path":"/{path}","cloud_run_service":"{APP}-{env}"}'
```

如果 `gcloud secrets versions access` 失敗，依錯誤分流：

- `PERMISSION_DENIED` / `403`：你還沒被加進 `ai_lab@cmoney.com.tw` group。
  → 告訴 PM：請聯繫 kevin_kuo@cmoney.com.tw 把你加進去。
- `NOT_FOUND` 或「Secret [...] not found」：secret 版本被停用或刪除。
  → 告訴 PM：先聯繫 kevin_kuo 確認 url-router 的 SM key 狀態。
- 其他：把原始錯誤訊息給 PM 看。

不論哪種 → 直接走 Step 5 Case A2（已部署的服務還在，但 URL 沒接上；顯示完整失敗資料 + 請 PM forward 給 kevin_kuo）。

### 4.5-2. 解析 helper 回應

| `status` 欄位 | 行為 |
|---|------|
| `"ok"` | 顯示 ✅ + `external_url` + `actions` 摘要（NEG / backend / routeRule 都建好）|
| `"already_bound"` | 顯示「已綁過，網址 X」（idempotent，沒變動）|
| `"rolled_back"` | helper 已自動把 url-map 還原成 bind 之前的狀態。走 Step 5 Case A2b — 顯示完整失敗資料給 PM（APP={APP}, env={env}, path={path}, Cloud Run service={APP}-{env}, helper 完整回應 JSON 含 error / restored_from 欄位），請 PM forward 給 kevin_kuo@cmoney.com.tw 協助。 |
| `cloud_run_not_found` | Cloud Run service 剛建好可能還在 propagate（常見）。等 30 秒後**自動重試一次**。重試一次仍失敗 → 走 Step 5 Case A2b（請 PM forward 失敗資料給 kevin_kuo；想自助可等 1-2 分鐘後手動重跑 Step 4.5）|
| `path_already_bound_to_different_value` | 該路徑已綁定到別的服務。走 Step 5 Case A2b — 顯示完整資料（`{path}` / env / 想綁的新服務={APP}-{env}），請 PM forward 給 kevin_kuo@cmoney.com.tw 協助 unbind 舊路由。PM 也可選擇重跑整個 skill 並在 Step 2-3.5 改用不同的 path。 |
| HTTP 4xx（其他 `code`） | 顯示對應錯誤；可能是 PM 輸入有誤或路徑衝突。走 Step 5 Case A2b，請 PM forward 給 kevin_kuo，同時 PM 也可檢查輸入後重跑 |
| HTTP 5xx 或 network error | 走 Step 5 Case A2b — 顯示完整失敗資料，請 PM forward 給 kevin_kuo@cmoney.com.tw |

---

## Step 5: 處理部署結果

### 部署成功

依 (env, prod 模式) 顯示對應訊息。本技能部署的服務一律對外，dev / prod 都會有對外網址（沒有「純內部」分支）。

#### Case A：dev 或 prod sub-path 模式

依 Step 4.5 的 helper 回應顯示對應結果。

**情況 A1：Step 4.5 helper bind 成功（status: ok / already_bound）**

網頁：
> ✅ 部署 + 接 URL 全部完成！
>
> 🌐 對外正式網址：https://ailab-{env}.cmoney.tw/{path}/
>    （helper 已驗證 HTTP 200）
>
> 📌 直連 URL（僅供 debug）：{cloudRunUrl}

後端 API：
**`{swaggerPath}` 對應規則**（Claude 在輸出前依 Step 2-2 偵測到的框架自動代換）：
.NET / NestJS → `/swagger`；FastAPI → `/docs`；Flask-RESTX → `/`；其他框架不確定 → 先填 `/swagger`，並在訊息末加一行「若 404 請改試 `/docs`」。

> ✅ 部署 + 接 URL 全部完成！
>
> 🌐 對外正式網址：
>    - API：https://ailab-{env}.cmoney.tw/{path}/
>    - API 文件：https://ailab-{env}.cmoney.tw/{path}{swaggerPath}
>
> 📌 直連 URL（僅供 debug）：{cloudRunUrl}

**情況 A2：helper 呼叫失敗（status: rolled_back / cloud_run_not_found / 5xx / network error 等）**

> ✅ 部署成功，但 URL 自動接線失敗。
>
> 📌 直連 URL（內部測試用，可立即使用）：{cloudRunUrl}
>
> ⚠️ helper 沒成功 bind 對外路徑。請把以下整段失敗資料 **forward 給 kevin_kuo@cmoney.com.tw** 協助處理：
>
> ```
> APP={APP}
> env={env}
> path=/{path}
> Cloud Run service={APP}-{env}
> Cloud Run 直連 URL={cloudRunUrl}
> helper 完整回應：
> <把 helper 回的 JSON / 錯誤訊息整段貼上>
> ```

> **執行端筆記**：目前 url-router-helper backend **沒有自動通知機制**，PM 需要手動 forward 上面這段失敗資料給 kevin_kuo。skill 的責任是把所有 debug 資料一次秀齊，PM 直接複製貼上即可，不用自己拼湊。

#### Case B：prod subdomain 模式

**網頁 / API 都適用：**

> 部署成功！Cloud Run service 已上線：
>
> 📌 直連 URL（subdomain LB setup 完成前都靠這個）：
>    {cloudRunUrl}
>
> 🌐 對外正式網址（canonical，待 kevin_kuo@cmoney.com.tw setup 後生效）：
>    https://{product}.cmoney.tw/
>    （後端 API 的文件位於 https://{product}.cmoney.tw{swaggerPath}；`{swaggerPath}` 規則同 Case A1）
>
>    ⚠️ subdomain 模式需 kevin_kuo@cmoney.com.tw 一次性 setup（DNS / cert / LB）。
>    請把以下三件事傳給 kevin_kuo@cmoney.com.tw：
>      - 想要的 subdomain：{product}.cmoney.tw
>      - Cloud Run service：{APP}-prod
>      - 確認 code 已**移除** basePath（或設為空字串）— subdomain 模式必要
>
>    kevin_kuo setup 完成前，subdomain 不會通，先用直連 URL 給內部測試。

### 部署失敗

**情況 A：build 失敗**

1. 檢查專案是否已有 `Dockerfile`：
   - **沒有** Dockerfile → 先確認 templates 目錄存在：
     ```bash
     [ -d ~/.claude/skills/ailab-cloudrun-deploy/templates/ ] || (echo "templates 目錄找不到，請聯繫 kevin_kuo" && exit 1)
     ```
     從 `~/.claude/skills/ailab-cloudrun-deploy/templates/` 複製對應的範本到原始碼目錄，自動重試一次
   - **已有** Dockerfile → 不覆蓋。顯示錯誤訊息給 PM，並問：
     > 你的專案已經有 Dockerfile 但 build 失敗。要：
     > 1. 跟我說錯誤訊息，我幫你看（推薦）
     > 2. 用 skill 內建的範本暫時取代你的 Dockerfile（會備份你原本的成 Dockerfile.bak）

2. 重試後仍失敗 → 進入「情況 B」

**情況 B：重試後仍失敗，或其他錯誤**

依錯誤訊息分流：

- `RESOURCE_EXHAUSTED` 或含 `Build quota`：
  > Cloud Build 同時建置數量已達 quota（同時 10 個）。請等 2-5 分鐘讓其他建置完成，再重試。
  > 如持續這個錯誤超過 10 分鐘，請聯繫 kevin_kuo。

- `PERMISSION_DENIED`（不該發生，因為 Step 1 已檢查）：
  > 中途權限被收回？請重新登入：`gcloud auth login`，然後重跑 skill。

- 其他錯誤：
  > 部署失敗了，錯誤訊息如下：
  > ```
  > {錯誤訊息}
  > ```
  > 建議將以上錯誤訊息截圖，傳給工程師協助處理。

---

## Dockerfile Templates 說明

`~/.claude/skills/ailab-cloudrun-deploy/templates/` 目錄下存放各種專案類型的 Dockerfile 範本，作為 **fallback** 使用。

當 Cloud Run 的自動建置（Buildpacks）失敗時，技能會自動從 `~/.claude/skills/ailab-cloudrun-deploy/templates/` 複製對應的 Dockerfile 到專案根目錄，然後重試部署。目前支援的範本：

- `Dockerfile.node` — Node.js 專案（多階段建構，npm ci + npm start）
- `Dockerfile.go` — Go 專案（多階段建構，編譯後用 distroless 執行）
- `Dockerfile.python` — Python 專案（pip install + python app.py）
- `Dockerfile.dotnet` — .NET 專案（多階段建構，dotnet publish + aspnet runtime）

使用者不需要知道 Dockerfile 的存在，整個流程對使用者是透明的。
