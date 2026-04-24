# plugin-ailab-cloudrun-deploy

部署網頁或後端服務到 Cloud Run，適合非工程師使用，全自動引導。

## 安裝

```
/plugin install ailab-cloudrun-deploy@cm-ailab-cc-plugins
```

## 使用方式

在專案目錄中說「部署到 Cloud Run」或「deploy to Cloud Run」即可觸發。

Skill 會自動引導你完成：
1. 前置檢查（gcloud 安裝、帳號登入、權限確認）
2. 偵測專案類型（網頁 / 後端 API）
3. 選擇環境（dev / prod）
4. 掃描環境變數
5. 執行部署
6. 回傳 URL（網頁）或 Swagger 連結（API）
