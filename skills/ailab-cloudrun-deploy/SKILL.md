---
name: ailab-cloudrun-deploy
description: >
  部署網頁或後端服務到 Google Cloud Run。適合非工程師使用，全自動引導。
  Use when: 使用者說「部署到 Cloud Run」、「deploy to Cloud Run」、「上版到 Cloud Run」等。
---

# Cloud Run 部署技能

本技能引導使用者將網頁應用程式或後端 API 部署到 Google Cloud Run，全程自動化，不需要 Docker 或 GCP 知識。

對外 URL 接線（path-based routing 到 ailab LB）由獨立 skill 處理，本 skill 只負責 Cloud Run 本身。

## 預設值

| 項目 | 預設值 |
|------|--------|
| GCP 專案 | `ailab-494105` |
| 區域 | `asia-east1` |
| 服務名稱 (測試) | `{資料夾名}-dev` |
| 服務名稱 (正式) | `{資料夾名}-prod` |
| 認證方式 | Google Group（使用者個人帳號） |
| VPC egress 出口 IP | dev / prod 各一條固定 IP（由 Cloud NAT 自動配發） |
| 對外 URL（候選） | `https://ailab-{env}.cmoney.tw/{APP}/` ※需 platform 加 LB path rule 才生效 |
| Swagger 路徑 | `/swagger` |

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
    exec -l $SHELL
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

- 登入後設定專案：
  ```bash
  gcloud config set project ailab-494105
  ```

### 1-3. 檢查部署權限

```bash
gcloud run services list --project=ailab-494105 --region=asia-east1 --limit=1 2>&1
```

- 如果出現權限錯誤（PERMISSION_DENIED），中斷流程並告知使用者：
  > 你的帳號目前沒有部署權限。請聯繫 kevin_kuo@cmoney.com.tw，請他幫你新增 Cloud Run 部署權限到 ailab-494105 專案。

---

## Step 2: 收集部署資訊

### 2-1. 部署來源

使用當前工作目錄（`pwd`）作為部署來源，服務名稱從當前資料夾名稱自動產生。

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
> 1. sub-path（預設、不需 IT 額外申請）—— `https://ailab-prod.cmoney.tw/{APP}/`
> 2. subdomain（例如 `tax-saving.cmoney.tw`）—— **需要聯繫 kevin_kuo@cmoney.com.tw 開通 URL map**：DNS A record、cert、URL map hostRule、backend service + Serverless NEG 都要先建好，subdomain 才會通；這些不是部署當下做的事，請先確認 IT 已經處理或同步發起申請

> ⚠️ 選 subdomain 但 IT 還沒處理完 → 部署完之後 subdomain 不會通，要等 IT 設定完成才能對外。在等的期間可以先用 Cloud Run 直連 URL（部署完後 skill 會給你）做內部測試。

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

收集所有環境變數，後續組成 `--set-env-vars` 參數。

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

但 next.config.ts 沒看到 basePath。HTML 內的 /_next/、/icons/ 等絕對
路徑會被 LB 當成 root 找不到 → 白屏。

請在 next.config.ts 加上：

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
你選擇的是 prod subdomain 模式（{product}.cmoney.tw），但 next.config.ts
仍然設了 basePath: '/{APP}'。

subdomain 模式下服務直接掛在 root，code 不能有 basePath，否則整站
404（因為 Next.js 期望 /{APP}/page 但實際收到 /page）。

最簡單修法：

  const nextConfig = {
    basePath: '',           // 或整行刪掉
    assetPrefix: '',        // 或整行刪掉
  }

更建議的彈性做法（同一份 code 同時支援 dev sub-path + prod subdomain，
不用每次切模式都改 code）：

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
            或 subdomain → 對外網址 https://my-app.cmoney.tw/（IT 已備好子網域）
路徑前綴：  /my-app  ✅  basePath 已驗證（sub-path 模式 / dev）
            或：  ✅  basePath 已驗證（subdomain 模式：code 沒設 basePath，符合期望）
            或：  ⏭️  使用者選擇 skip（純內部 / 只用 Cloud Run 直連 URL）
            或：  ⚠️  無法自動驗證，使用者已確認自行負責
環境變數：  DATABASE_URL（已設定）
            API_KEY（已設定）
==============================

確認以上資訊無誤嗎？(Y/n)
```

注意：環境變數只顯示 key 名稱，不顯示值（避免洩漏敏感資訊）。

使用者確認後才繼續。如果使用者要修改，回到對應步驟重新收集。

---

## Step 4: 執行部署

根據環境和設定組合 `gcloud run deploy` 指令。

```bash
gcloud run deploy {name}-{dev|prod} \
  --source {path} \
  --project ailab-494105 \
  --region asia-east1 \
  --allow-unauthenticated \
  --network=default \
  --subnet=ailab-asia-east1-{dev|prod} \
  --vpc-egress=all-traffic
```

`--network` / `--subnet` / `--vpc-egress` 三個 flag 把服務的對外流量導到 Cloud NAT 固定 IP，後端白名單服務才認得。

### 有環境變數時

加上 `--set-env-vars` 參數：

```bash
gcloud run deploy {name}-{env} \
  --source {path} \
  --project ailab-494105 \
  --region asia-east1 \
  --allow-unauthenticated \
  --network=default \
  --subnet=ailab-asia-east1-{env} \
  --vpc-egress=all-traffic \
  --set-env-vars "KEY1=value1,KEY2=value2"
```

部署成功後取得 `{cloudRunUrl}`（`status.url`）。

---

## Step 5: 處理部署結果

### 部署成功

依 (env, prod 模式) 顯示對應訊息。

#### Case A：dev 或 prod sub-path 模式

**網頁專案：**

> 部署成功！你的網頁已上線：
>
> 📌 直連 URL（僅供你 / 內部 debug，主用網址用下面那個）：
>    {cloudRunUrl}
>
> 🌐 對外正式網址（canonical）：
>    https://ailab-{env}.cmoney.tw/{APP}/
>    ⚠️ 第一次部署時，需要 platform admin 在 ailab LB 加 routeRule（**不 rewrite，
>    直接 passthrough**，因為你 code 已設好 basePath）。請把以下資訊傳給
>    kevin_kuo@cmoney.com.tw：
>      - APP 名稱：{APP}
>      - 環境：{env}
>      - 模式：sub-path
>      - Cloud Run service：{name}-{env}
>    一次性設定，下次部署不用再找。

**後端 API 專案：**

> 部署成功！你的 API 已上線：
>
> 📌 直連 URL（僅供內部呼叫測試）：
>    - API 位址：{cloudRunUrl}
>    - API 文件：{cloudRunUrl}/swagger
>
> 🌐 對外正式網址（canonical）：
>    - API 位址：https://ailab-{env}.cmoney.tw/{APP}/
>    - API 文件：https://ailab-{env}.cmoney.tw/{APP}/swagger
>    ⚠️ 同上，請把上述資訊（APP / env / 模式: sub-path / Cloud Run service）
>    傳給 kevin_kuo@cmoney.com.tw 開 routeRule。

#### Case B：prod subdomain 模式

**網頁 / API 都適用：**

> 部署成功！Cloud Run service 已上線：
>
> 📌 直連 URL（subdomain LB setup 完成前都靠這個）：
>    {cloudRunUrl}
>
> 🌐 對外正式網址（canonical，待 IT setup 後生效）：
>    https://{product}.cmoney.tw/    （API 文件位於 .../swagger）
>
>    ⚠️ subdomain 模式比 sub-path 多以下 IT 一次性設定：
>      1. **DNS** A record：`{product}.cmoney.tw` → ailab-prod LB IP `34.49.91.69`
>      2. **Cert**：managed cert（或上傳），掛到 `prod-lb-https-proxy`
>      3. **Backend service + Serverless NEG**：建立指到 `{name}-prod` Cloud Run 的 NEG
>         與對應 backend service `{name}-prod-backend`
>      4. **URL map hostRule**：在 `prod-lb-urlmap` 加 `{product}.cmoney.tw` 的 hostRule
>         + pathMatcher（**defaultService 直接指向新 backend，不 rewrite**）
>      5. （建議）在 `ailab-prod-matcher` 加 301 redirect rule：
>         `/{APP}` → `https://{product}.cmoney.tw/` 避免雙頭服務
>
>    請把以下資訊傳給 kevin_kuo@cmoney.com.tw：
>      - 想要的 subdomain：{product}.cmoney.tw
>      - Cloud Run service：{name}-prod
>      - 模式：subdomain
>      - 確認 code 已**移除** basePath（或設為空字串）— subdomain 模式必要
>
>    IT setup 完成前，subdomain 不會通，先用直連 URL 給內部測試。

### 部署失敗

**情況 A：沒有 Dockerfile 且出現 build 錯誤**

1. 根據偵測到的專案類型，從 `templates/` 目錄複製對應的 Dockerfile 到原始碼目錄
2. 自動重試部署一次（使用相同的 `gcloud run deploy` 指令）

**情況 B：重試後仍然失敗，或其他錯誤**

- 顯示錯誤訊息並建議：
  > 部署失敗了，錯誤訊息如下：
  > ```
  > {錯誤訊息}
  > ```
  > 建議將以上錯誤訊息截圖，傳給工程師協助處理。

---

## Dockerfile Templates 說明

`templates/` 目錄下存放各種專案類型的 Dockerfile 範本，作為 **fallback** 使用。

當 Cloud Run 的自動建置（Buildpacks）失敗時，技能會自動從 templates 複製對應的 Dockerfile 到專案根目錄，然後重試部署。目前支援的範本：

- `Dockerfile.node` — Node.js 專案（多階段建構，npm ci + npm start）
- `Dockerfile.go` — Go 專案（多階段建構，編譯後用 distroless 執行）
- `Dockerfile.python` — Python 專案（pip install + python app.py）
- `Dockerfile.dotnet` — .NET 專案（多階段建構，dotnet publish + aspnet runtime）

使用者不需要知道 Dockerfile 的存在，整個流程對使用者是透明的。
