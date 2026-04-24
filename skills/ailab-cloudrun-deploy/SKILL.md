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

- 測試機 → 服務名稱後綴 `-dev`
- 正式機 → 服務名稱後綴 `-prod`

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

---

## Step 3: 確認摘要

在執行部署前，顯示摘要讓使用者確認：

```
========== 部署確認 ==========
服務名稱：  my-app-dev
專案類型：  網頁 / 後端 API
環境：      測試機 (dev) — 公開存取
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
  --allow-unauthenticated
```

### 有環境變數時

加上 `--set-env-vars` 參數：

```bash
gcloud run deploy {name}-{env} \
  --source {path} \
  --project ailab-494105 \
  --region asia-east1 \
  --allow-unauthenticated \
  --set-env-vars "KEY1=value1,KEY2=value2"
```

---

## Step 5: 處理部署結果

### 部署成功

**網頁專案：**
- 回傳 Service URL：
  > 部署成功！你的網頁已上線：
  > {Service URL}

**後端 API 專案：**
- 回傳 Service URL 和 Swagger 文件連結：
  > 部署成功！你的 API 已上線：
  > - API 位址：{Service URL}
  > - API 文件：{Service URL}/swagger

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
