---
name: ailab-cloudrun-deploy
description: >
  部署網頁或後端服務到 Google Cloud Run。適合非工程師使用，全自動引導。
  Use when: 使用者說「部署到 Cloud Run」、「deploy to Cloud Run」、「上版到 Cloud Run」等。
---

# Cloud Run 部署技能

本技能引導使用者將網頁應用程式或後端 API 部署到 Google Cloud Run，全程自動化，不需要 Docker 或 GCP 知識。

對外 URL 接線（sub-path 模式）在 Step 4.5 自動執行（透過 `url-router-helper`，不需找 platform admin）。
subdomain 模式仍需 IT 一次性設定，詳見 Step 5 Case B。

## 預設值

| 項目 | 預設值 |
|------|--------|
| GCP 專案 | `ailab-494105` |
| 區域 | `asia-east1` |
| 服務名稱 (測試) | `{資料夾名}-dev` |
| 服務名稱 (正式) | `{資料夾名}-prod` |
| 認證方式 | Google Group（使用者個人帳號） |
| VPC egress 出口 IP | dev / prod 各一條固定 IP（由 Cloud NAT 自動配發） |
| 對外 URL（sub-path）| `https://ailab-{env}.cmoney.tw/{APP}/`（部署完 skill 自動接 LB，不需找 platform admin） |
| URL Router Helper | `https://url-router-helper-557076811903.asia-east1.run.app`（內部 service，SM key 認證） |
| Swagger / API 文件路徑 | 因框架而異：.NET / NestJS = `/swagger`；FastAPI = `/docs`；Flask-RESTX = `/`；其他請依框架預設 |

> **維運備註（給 platform admin 看）**：上方 helper URL 是 SoT。Step 4.5 / Step 5 內的指令模板雖然各自寫死同一個 URL，但若 helper 重新部署造成 URL 變動，請改本表格 + 同步更新 Step 4.5-3 與 Step 5 Case A2 的 `HELPER_URL=...` 字串，然後跑 `cm-ailab-mp:update` 重發 skill。

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
# Secret Manager 讀取權限（Step 4.5 自動接 URL 會用到）
gcloud secrets versions access latest --secret=url-router-key --project=ailab-494105 >/dev/null 2>&1
```

Cloud Run 或 Cloud Build 任一出現 `PERMISSION_DENIED`，中斷流程：

> 你的帳號目前沒有 [Cloud Run / Cloud Build] 部署權限。請聯繫 kevin_kuo@cmoney.com.tw 開通。

如果只有 Secret Manager 那條失敗（PM 沒在 `ai_lab@cmoney.com.tw` group 或 secret 不存在）：
> 你拿不到 url-router-key（沒在 ai_lab group 或 secret 暫時不可讀）。
> 部署本身還能跑，但 Step 4.5（自動接 URL）會跳過，部署完要手動聯繫 kevin_kuo@cmoney.com.tw 接 URL。
> 要繼續嗎？(Y/n)

PM 選 Y → 繼續部署但記住 Step 4.5 要 skip；選 N → 中斷請 PM 先解決權限。

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

**無法判定：**
- 詢問使用者：
  > 無法自動判斷這是網頁還是後端 API。請問這是：
  > 1. 網頁（前端頁面）
  > 2. 後端 API（提供資料的伺服器）

### 2-3. 部署環境

詢問使用者要部署到哪個環境：

> 要部署到哪個環境？
> 1. 測試機 (dev) — 用於內部測試
> 2. 正式機 (prod) — 正式上線環境

- 測試機 → 服務名稱後綴 `-dev`，**一律走 sub-path** `https://ailab-dev.cmoney.tw/{APP}/`（測試機不開 subdomain）
- 正式機 → 服務名稱後綴 `-prod`，需要再問一次想走哪種對外網址模式

如果使用者選 **正式機 (prod)**，再問：

> 正式機要走哪一種對外網址？
> 1. sub-path（預設、不需額外申請）—— `https://ailab-prod.cmoney.tw/{APP}/`
> 2. subdomain（例如 `tax-saving.cmoney.tw`）—— **需要聯繫 kevin_kuo@cmoney.com.tw 一次性 setup**（DNS / cert / LB 由他處理）；不是部署當下做的事，請先確認他已處理或同步發起申請

> ⚠️ 選 subdomain 但 kevin_kuo@cmoney.com.tw 還沒處理完 → 部署完之後 subdomain 不會通，要等設定完成才能對外。在等的期間可以先用 Cloud Run 直連 URL（部署完後 skill 會給你）做內部測試。

如果使用者選 **subdomain**，再問：

> 你的子網域 prefix 是什麼？預設：{APP}
> （絕大多數用預設即可。只有品牌改名才需要改，例如資料夾叫 `tax-better` 但對外想叫 `tax-saving` → `tax-saving.cmoney.tw`）

PM 按 Enter 用預設（`{product} = {APP}`），或輸入自訂值。

把答案綁成 `{product}` 變數，後續 Step 3 確認 / Step 5 Case B 會用到。
prefix 必須符合 DNS 規則：小寫英文/數字/連字號，不可開頭或結尾用連字號，最長 63 字元。
偵測到不合規 → 重新問。

兩個模式對 code 的要求剛好相反，後面 Step 2-5 會依這個選擇驗證：

| 模式 | 對外 URL bar 顯示 | code 必須的 basePath |
|---|---|---|
| dev（永遠）| `ailab-dev.cmoney.tw/{APP}/...` | `'/{APP}'` |
| prod sub-path | `ailab-prod.cmoney.tw/{APP}/...` | `'/{APP}'` |
| prod subdomain | `{product}.cmoney.tw/...` | **不設**（或空字串） |

兩者皆使用 `--allow-unauthenticated`（公開存取），認證由應用程式自行處理。

### 2-4. 環境變數

自動掃描專案中可能需要的環境變數：

1. 檢查是否有 `.env`、`.env.example`、`.env.sample` 等檔案
2. 掃描程式碼中引用的環境變數（如 `process.env.XXX`、`os.Getenv("XXX")`、`os.environ["XXX"]`、`Environment.GetEnvironmentVariable("XXX")`）
3. 整理出所有需要設定的環境變數清單

將找到的環境變數列出，詢問使用者：

> 我在專案中找到以下需要設定的項目：
> - DATABASE_URL（目前沒有值）
> - API_KEY（目前沒有值）
>
> 請提供這些設定的值，或告訴我哪些不需要設定。

如果專案中有 `.env` 檔案且包含值，直接使用該檔案的值，並顯示讓使用者確認。
如果完全找不到任何環境變數，跳過此步驟。

**⚠️ 注意：`.env` 內容會直接送到 Cloud Run 當環境變數**

請確認 `.env` 中的值都是**正式機 / 雲端適用**的，不是 localhost 開發專用。常見要檢查的：

- `DATABASE_URL` 不能是 `localhost:5432` → 用 Cloud SQL / GKE pgbouncer endpoint
- `REDIS_URL` 同理
- `API_BASE_URL` 不能指向 `127.0.0.1`、私有 IP
- 任何 `*_HOST` / `*_URL` 是 dev-only 的服務名稱

如果 `.env` 裡混了 dev-only 值，**逐項問 PM** 真實的雲端值是什麼，不要照搬。

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

本步驟根據 Step 2-3 選好的「環境 + (prod 模式)」，驗證 code 的 basePath 是否設正確：

| 環境 + 模式 | 對外 URL | code 期望的 basePath |
|---|---|---|
| dev（一律 sub-path）| `https://ailab-dev.cmoney.tw/{APP}/...` | `'/{APP}'` |
| prod **sub-path** | `https://ailab-prod.cmoney.tw/{APP}/...` | `'/{APP}'` |
| prod **subdomain** | `https://{product}.cmoney.tw/...` | **空字串 / 不設** |

`{APP}` = 當前資料夾名（service name 去掉 `-dev`/`-prod` 後綴）。

**為什麼這個檢查重要：**
- **sub-path 模式**下，LB 把 path 原封轉給 Cloud Run（**不 rewrite**）。code 沒設 basePath → HTML 內的 `/_next/...`、`/icons/...` 等絕對路徑被當 root → LB 找不到 → **白屏**。API 同理：route 掛在 `/` 但呼叫端打 `/{APP}/...` → 404。
- **subdomain 模式**下，subdomain 的流量**一樣經過同一個 ailab LB**（不是 DNS 直連 Cloud Run），URL map 多一條 hostRule 把它打到對應 backend，path 原樣 passthrough（不 rewrite）。如果 code 還留著 `basePath: '/{APP}'` → 服務只認 `/{APP}/page`，但 LB 傳過來的是 `/page`（subdomain 沒帶前綴）→ **整站 404**。

依照偵測到的框架，檢查對應設定檔的 basePath 欄位：

| 框架 | 偵測訊號 | 必須設定 |
|---|---|---|
| Next.js | `next.config.{js,ts,mjs,cjs}` 存在 | `basePath: '/{APP}'`（強烈建議連 `assetPrefix: '/{APP}'` 也設） |
| Nuxt 3 | `nuxt.config.{ts,js}` 存在且 deps 有 `nuxt` v3+ | `app.baseURL: '/{APP}/'` |
| Nuxt 2 | `nuxt.config.{ts,js}` 存在且 deps 有 `nuxt` v2 | `router.base: '/{APP}/'` |
| Vite (Vue/React/Solid) | `vite.config.{ts,js}` 存在 | `base: '/{APP}/'` |
| Create React App | `package.json` 有 `react-scripts` | `"homepage": "/{APP}"` 寫在 package.json |
| SvelteKit | `svelte.config.js` 存在 | `kit.paths.base: '/{APP}'` |
| Angular | `angular.json` 存在 | `index.html` 內有 `<base href="/{APP}/">` 或 build flag `--base-href /{APP}/` |
| 純靜態 HTML | 只有 `index.html`，無 build 工具 | 所有 `src` / `href` 用**相對路徑**（不能 `/xxx` 開頭）；或在 `<head>` 加 `<base href="/{APP}/">` |
| FastAPI | `main.py` / `app.py` import `fastapi` | `FastAPI(root_path='/{APP}')` 或部署參數 `--root-path /{APP}` |
| Flask | import `flask` | 設 `APPLICATION_ROOT='/{APP}'` 並用 `werkzeug.middleware.proxy_fix.ProxyFix` |
| Express | import `express` | 所有 router 掛在 prefix 下：`app.use('/{APP}', router)` |
| ASP.NET Core | `Program.cs` 或 `Startup.cs` | `app.UsePathBase("/{APP}")`（在 `app.UseRouting()` 之前） |
| Spring Boot | `application.{yaml,yml,properties}` 存在 | `server.servlet.context-path: /{APP}` |
| Go (gin / chi / gorilla / std net/http) | `main.go` 引用對應 router | mux subrouter 加 prefix，或全部 handler 註冊在 `/{APP}/...` 下 |

偵測到框架後，把對應的設定檔名稱（`next.config.ts` / `vite.config.ts` / `nuxt.config.ts` /
`svelte.config.js` / `angular.json` / `application.yaml` / `Program.cs` 等）綁成 `{configFile}`，
後續 FAIL 訊息會引用。

**為何各框架尾斜線不一樣？** 這是各框架對 basePath 的內部慣例，不是寫錯。請嚴格按表格填，
不要自己加減 `/`。例如 Next.js `basePath: '/{APP}'`（無尾斜線）vs Vite `base: '/{APP}/'`（有尾斜線），
是各自 routing 引擎的歷史 API，混用會出 404 / 重定向迴圈。

**驗證流程：**

1. 計算 `{APP}` = 資料夾名
2. 取得 Step 2-3 的 (env, mode) → 算出「期望的 basePath 值」：
   - dev / prod sub-path → 期望 `/{APP}`
   - prod subdomain → 期望 `''`（空字串 / 不設）
3. 用框架表偵測專案類型
4. 讀對應 config，grep 對應欄位的值（可能是字面字串，也可能是 `process.env.XXX || ''` 之類的 build-time 動態值）
5. 比對：
   - ✅ **PASS**：實際值 = 期望值 → 顯示綠勾，繼續
     - 若實際值是 `process.env.NEXT_PUBLIC_BASE_PATH || ''` 之類的動態值，視為 PASS，但要在 deploy 階段確保 build env var 設對（sub-path 模式要 `--set-build-env-vars NEXT_PUBLIC_BASE_PATH=/{APP}`，subdomain 模式則不傳）
   - ❌ **FAIL**：實際值 ≠ 期望值 → 中斷部署，顯示具體修法（見下面 Case A / Case B）
   - ⚠️ **WARN**：偵測不出框架類型 → 列出常見設定方式請使用者自己確認，問 `(Y/n)` 是否仍要繼續

**FAIL Case A — sub-path 模式，但 code 沒設 basePath（最常見）：**

```
========== ❌ basePath 驗證失敗 (sub-path 模式) ==========
你選擇的是 sub-path 模式，對外 URL 會是：
  https://ailab-{env}.cmoney.tw/{APP}/

但 {configFile} 沒看到 basePath。HTML 內的 /_next/、/icons/ 等絕對
路徑會被 LB 當成 root 找不到 → 白屏。

請在 `{configFile}` 加上（以下以 Next.js 為例；其他框架請依 Step 2-5 表格的對應設定）：

  const nextConfig = {
    basePath: '/{APP}',
    assetPrefix: '/{APP}',
  }

改完 git commit 後再重新部署。
==========================================================
```

**FAIL Case B — prod subdomain 模式，但 code 還留 basePath：**

```
========== ❌ basePath 驗證失敗 (subdomain 模式) ==========
你選擇的是 prod subdomain 模式（{product}.cmoney.tw），但 `{configFile}`
仍然設了 basePath（值為 `/{APP}` 或類似）。

subdomain 模式下服務直接掛在 root，code 不能有 basePath，否則整站
404（因為框架期望 /{APP}/page 但實際收到 /page）。

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
  - dev (sub-path):     --set-build-env-vars NEXT_PUBLIC_BASE_PATH=/{APP}
  - prod (sub-path):    --set-build-env-vars NEXT_PUBLIC_BASE_PATH=/{APP}
  - prod (subdomain):   不傳這個 env var
==========================================================
```

**其他框架的彈性做法（同樣概念，env var 控制 basePath）：**

| 框架 | 設定方式 |
|---|---|
| Vite | `base: process.env.VITE_BASE_PATH || '/'`，deploy 帶 `--set-build-env-vars VITE_BASE_PATH=/{APP}/` |
| Nuxt 3 | `app.baseURL: process.env.NUXT_APP_BASE_URL || '/'`，deploy 帶 `--set-build-env-vars NUXT_APP_BASE_URL=/{APP}/` |
| SvelteKit | `kit.paths.base: process.env.BASE_PATH || ''`（注意 SvelteKit basePath 不能有尾斜線） |
| Spring Boot | `server.servlet.context-path: ${BASE_PATH:/}`，runtime env var `BASE_PATH=/{APP}` |

其他框架原則一樣：basePath 設成 `process.env.XXX || ''`，dev/sub-path 模式 deploy 時帶對應 env var。

**WARN 範例輸出：**

```
========== ⚠️ 無法自動驗證 basePath ==========
偵測不出你的網頁框架是哪一個。請自己確認 code 符合所選模式：

  - 你選擇的模式是 [dev / prod sub-path / prod subdomain]
  - sub-path 模式 → code 必須設 /{APP} 前綴（例如 Next.js basePath、
    Vite base、Spring Boot context-path 等）
  - subdomain 模式 → code 不能設 basePath（要在 root serve）

如果設錯，部署後可能整站白屏 / 404。

要繼續部署嗎？(Y/n)
==============================================
```

**唯一可以 skip 這個檢查的情境：** 服務根本不對公網開放（純 internal job / 只用 Cloud Run 自動產生的 `*.run.app` 直連 URL，由其他內部服務呼叫）。詢問：

> 這個服務會對外提供服務嗎？
> 1. 會（預設） — 我幫你檢查 basePath
> 2. 不會，是純內部用途，只用 Cloud Run 直連 URL — skip 這個檢查

---

## Step 3: 確認摘要

在執行部署前，顯示摘要讓使用者確認：

```
========== 部署確認 ==========
服務名稱：  my-app-dev
專案類型：  網頁 / 後端 API
環境：      測試機 (dev) — 公開存取
                  或   正式機 (prod) — 公開存取
正式機模式：（只在 env=prod 時出現這行）
            sub-path  → 對外網址 https://ailab-prod.cmoney.tw/my-app/
            或 subdomain → 對外網址 https://my-app.cmoney.tw/（kevin_kuo@cmoney.com.tw 已備好子網域）
路徑前綴：  /my-app  ✅  basePath 已驗證（sub-path 模式 / dev）
            或：  ✅  basePath 已驗證（subdomain 模式：code 沒設 basePath，符合期望）
            或：  ⏭️  使用者選擇 skip（純內部 / 只用 Cloud Run 直連 URL）
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

把這個值綁成 `{cloudRunUrl}` 變數，**Step 4.5 / Step 5 都會用**（不論你走 Case A1 / A2 / B / C 哪個分支）。

---

## Step 4.5: 自動接對外網址（**僅 sub-path 模式**）

**前置條件**：本步驟**只在以下情境執行**：
- env=dev（永遠 sub-path）
- env=prod 且模式=sub-path
- Step 1-3 Secret Manager 檢查通過（PM 拿得到 `url-router-key`）

**跳過整個 Step 4.5、直接進 Step 5 的情境**（任一即跳過）：
- PM 在 Step 2-5 選了「純內部」（不對外）→ Step 5 走 Case C
- Step 2-3 選了 prod subdomain 模式 → Step 5 走 Case B
- Step 1-3 Secret Manager 檢查失敗、PM 選擇繼續 → Step 5 走 Case A2（並提醒 PM 拿到 group 權限後手動接 URL）

### 4.5-1. 詢問對外路徑

> 對外路徑要叫什麼？預設：{APP}（絕大多數用預設即可。tax-better → tax-saving 那種品牌改名才需要改）

把答案綁成 `{path}`，**不含**開頭斜線（`tax-saving`，不是 `/tax-saving`）。**驗證 regex** `^[a-z][a-z0-9-]*(/[a-z][a-z0-9-]*)*$`，不過則重新問。

路徑只能用小寫英文、數字、連字號（例如 `tax-saving`），不能有大寫、空格、開頭斜線或特殊字元。

### 4.5-2. 詢問是否自動接

> 確認要把這個服務接到 https://ailab-{env}.cmoney.tw/{path}/ 嗎？(Y/n)
> （helper 會自動配置 LB routeRule，需 1-6 分鐘）

如果 PM 選 N，跳過此步、直接顯示 Step 5 Case A 的「未接 URL」變體（保留「自己呼叫 url-router-helper 或聯繫 kevin_kuo」的選項）。

### 4.5-3. 拿 SM key + 呼叫 helper

告訴 PM：

> 正在請 url-router-helper 處理綁定（建 NEG / backend / 加 LB 規則 / 等 LB propagate / 驗 200）...
> 通常 1-6 分鐘。請勿關閉終端。

然後執行：

```bash
KEY=$(gcloud secrets versions access latest --secret=url-router-key --project=ailab-494105)
EMAIL=$(gcloud config get-value account 2>/dev/null)
HELPER_URL=https://url-router-helper-557076811903.asia-east1.run.app

curl -s --max-time 360 -X POST "$HELPER_URL/bind" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $KEY" \
  -H "X-User-Email: $EMAIL" \
  -d '{"app":"{APP}","env":"{env}","path":"/{path}","cloud_run_service":"{APP}-{env}"}'
```

如果 `gcloud secrets versions access` 失敗，依錯誤分流：

- `PERMISSION_DENIED` / `403`：你還沒被加進 `ai_lab@cmoney.com.tw` group。
  → 告訴 PM：請聯繫 kevin_kuo@cmoney.com.tw 把你加進去。
- `NOT_FOUND` 或「Secret [...] not found」：secret 版本被停用或刪除。
  → 告訴 PM：先聯繫 kevin_kuo 確認 `url-router-key` 狀態。
- 其他：把原始錯誤訊息給 PM 看。

不論哪種 → 給選項「先跳過 Step 4.5，後續手動處理」。

### 4.5-4. 解析 helper 回應

| `status` 欄位 | 行為 |
|---|------|
| `"ok"` | 顯示 ✅ + `external_url` + `actions` 摘要（NEG / backend / routeRule 都建好）|
| `"already_bound"` | 顯示「已綁過，網址 X」（idempotent，沒變動）|
| `"rolled_back"` | helper 已自動把 url-map 還原成 bind 之前的狀態。告訴 PM：「綁定失敗已自動回滾。請把以下完整訊息傳給 kevin_kuo@cmoney.com.tw 協助：APP={APP}, env={env}, path={path}, Cloud Run service={APP}-{env}, helper 完整回應 JSON（含 error / restored_from 欄位）」 |
| `cloud_run_not_found` | Cloud Run service 剛建好可能還在 propagate（常見）。等 30 秒後**自動重試一次**。重試一次後仍失敗，告知 PM「服務不存在或剛建好還沒 ready，等 1-2 分鐘後可手動重跑 Step 4.5（看本 SKILL.md 的 fallback 區塊），或聯繫 kevin_kuo@cmoney.com.tw」 |
| `path_already_bound_to_different_value` | 該路徑已綁定到別的服務。告知 PM：請把以下資訊傳給 kevin_kuo@cmoney.com.tw 協助 unbind 舊路由：path={path}, env={env}, 你想綁的新服務={APP}-{env}。或者改用不同的 path 重跑 Step 4.5-1。 |
| HTTP 4xx（其他 `code`） | 顯示對應錯誤、提示 PM 檢查輸入或先 unbind 既有綁定 |
| HTTP 5xx 或 network error | fallback「自動接失敗，請把 APP / env / Cloud Run service 傳給 kevin_kuo@cmoney.com.tw」|

---

## Step 5: 處理部署結果

### 部署成功

依 (env, prod 模式) 顯示對應訊息。

#### Case C: 純內部 / 不對外（Step 2-5 選了 skip）

> ✅ 部署成功，純內部服務，不接對外網址。
>
> 📌 Cloud Run 直連 URL（給其他內部服務 / CI 呼叫）：
>    {cloudRunUrl}
>
> 此服務未綁到 ailab LB，只能透過上面的直連 URL 存取。
> 之後若改為對外開放，重跑 `/ailab-cloudrun-deploy` 並選擇 sub-path / subdomain 模式即可。

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

**情況 A2：PM 在 Step 4.5 選 N（跳過自動接），或 helper 失敗，或 Step 1-3 SM 檢查失敗 PM 選擇繼續**

> ✅ 部署成功，但對外網址未自動接上。
>
> 📌 直連 URL（內部測試用）：{cloudRunUrl}
>
> 🌐 想接到 https://ailab-{env}.cmoney.tw/{path}/ 的話，**有兩個選項：**
>    1. 之後再執行 `/ailab-cloudrun-deploy` 重跑、或叫 url-router-helper：
>       ```
>       KEY=$(gcloud secrets versions access latest --secret=url-router-key --project=ailab-494105)
>       curl -X POST https://url-router-helper-557076811903.asia-east1.run.app/bind \
>         -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
>         -d '{"app":"{APP}","env":"{env}","path":"/{path}","cloud_run_service":"{APP}-{env}"}'
>       ```
>    2. 把以下資訊傳給 kevin_kuo@cmoney.com.tw：APP={APP}, env={env}, 模式=sub-path, Cloud Run service={APP}-{env}

**注意：** 如果是 Step 1-3 SM 檢查失敗的路徑進來（Step 4.5-1 沒跑、`{path}` 未綁定），把上方所有 `{path}` 預設成 `{APP}`（或先問 PM 想要的對外路徑後再給訊息）。

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
