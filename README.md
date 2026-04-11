# Goost — ADK Go Telegram Bot

本專案是一個使用 [ADK Go](https://github.com/google/adk-go) 建構的 Telegram Bot，主要作為工作坊教學用途，展示 Agent 開發的核心技術。

## 📍 學習目標

透過本專案，你將能學到：
- **ADK Go 的核心概念**：Agent、Runner、Session、Tool
- **System Prompt 管理**：使用 dotPrompt 的最佳實踐
- **Agent 除錯與調教**：使用 ADK Dev UI 加速開發
- **系統整合**：如何整合 Telegram 作為 Agent 的使用者介面

## 🚀 快速開始

如果你想跟著工作坊的步驟，實際安裝環境並將機器人跑起來，請參考完整的教學指南：
👉 **[閱讀 ONBOARDING.md 開始動手做](./ONBOARDING.md)**

## 🛠️ 採用技術

本專案綜合使用了以下多項技術：

### 1. ADK Go (Agent Development Kit)
使用 Google 的 ADK Go v1.0.0 建構 agent，核心元件：
- **`llmagent`** — 封裝 LLM agent 的 instruction、tools、callbacks
- **`runner.Runner`** — 管理 agent 執行迴圈，自動處理 multi-turn conversation
- **`session.InMemoryService`** — 記憶體內 session 管理，自動維護每位使用者的對話歷史

### 2. Dotprompt
使用 [google/dotprompt](https://github.com/google/dotprompt) 管理 system prompt：
- `.prompt` 檔案以 YAML frontmatter 定義 model 名稱等 metadata
- Handlebars 模板語法支援 `{{role "system"}}` 等 role markers
- Prompt 與程式碼分離，方便迭代調整

### 3. ADK Go Dev UI
ADK Go 內建開發者 UI，可用於本地測試與除錯 agent 行為，方便直觀地觀察 Agent 決策過程。

### 4. Custom Tool — `get_current_time`
使用 `functiontool.New` 建立自訂 tool，讓 LLM 取得正確的當前時間，避免幻覺：
- 支援 IANA timezone 參數（預設 `Asia/Taipei`）
- 回傳 RFC3339 格式的時間字串

### 5. MCP (Model Context Protocol)
透過 ADK Go 的 `mcptoolset` 整合外部 MCP server：
- 使用 `mcp.CommandTransport` 透過 stdio 啟動子行程
- 目前掛載 **chrome-devtools-mcp**，提供瀏覽器操作能力
- Lazy connection — 只在 LLM 需要時才啟動 MCP server

## 📁 專案結構

```text
.
├── agents/goost/agent.prompt   # Dotprompt system prompt
├── cmd/telegram/main.go        # 主程式入口
├── template.env                # 環境變數範本
└── go.mod
```
