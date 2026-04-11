# ADK Go 教學指南

## 什麼是 ADK Go？

[ADK (Agent Development Kit)](https://google.github.io/adk-docs/) 是 Google 推出的 Agent 開發框架  
提供 Python、Java、Go、TS/JS 四種語言版本

ADK Go 的核心概念：

| 概念 | 說明 |
|------|------|
| **Agent** | AI 代理人，定義 instruction、model、tools |
| **Tool** | Agent 可以呼叫的函式，讓 LLM 能與外部世界互動 |
| **Runner** | 管理 Agent 的執行迴圈，處理 multi-turn 對話 |
| **Session** | 維護每位使用者的對話歷史與狀態 |

整體架構：

```
使用者 → Transport (Telegram / Dev UI / Console) → Runner → Agent → LLM (Gemini)
                                                      ↓
                                                   Tools / MCP
```

---

## 1. 建立 Agent

Agent 是 ADK 的核心，透過 `llmagent.New` 建立：

```go
import (
    "google.golang.org/adk/agent/llmagent"
    "google.golang.org/adk/model/gemini"
    "google.golang.org/genai"
)

// 建立 Gemini model
model, err := gemini.NewModel(ctx, "gemini-2.5-flash", &genai.ClientConfig{
    Backend: genai.BackendGeminiAPI,
    APIKey:  os.Getenv("GOOGLE_GENAI_API_KEY"),
})

// 組裝 Agent
a, err := llmagent.New(llmagent.Config{
    Name:        "agent",
    Model:       model,
    Description: "A helpful AI assistant.",
    Instruction: instruction,       // system prompt 文字
    Tools:       []tool.Tool{...},  // 自訂 tools
    Toolsets:    []tool.Toolset{},  // MCP toolsets
})
```

`llmagent.Config` 的重點欄位：
- `Instruction` — Agent 的 system prompt，決定它的行為和個性
- `Tools` — 個別的 tool（如 `functiontool`）
- `Toolsets` — 一組 tools 的集合（如 MCP server 提供的所有 tools）

---

## 2. Dev UI — 內建的開發者介面

ADK Go 內建了一個開發者 UI，可以在本地瀏覽器中測試 Agent  
不需要設定 Telegram，只要有 Gemini API Key 就能用

### 怎麼運作？

關鍵在 `main.go` 的這段邏輯：

```go
import (
    "google.golang.org/adk/cmd/launcher"
    "google.golang.org/adk/cmd/launcher/full"
)

// 如果有帶子命令（web / console），交給 ADK launcher 處理
// 否則啟動 Telegram Bot
if len(os.Args) > 1 {
    l := full.NewLauncher()
    return l.Execute(ctx, &launcher.Config{
        AgentLoader:    agent.NewSingleLoader(a),
        SessionService: sessService,
    }, os.Args[1:])
}

return runTelegram(ctx, a, sessService)
```

`full.NewLauncher()` 提供了兩個子命令：

```bash
# Dev UI — 啟動 Web 介面
go run ./cmd/telegram/ web

# Console — CLI 互動模式
go run ./cmd/telegram/ console
```

### Dev UI 能做什麼？

- 直接跟 Agent 對話，即時看到回應
- 觀察每一次 Tool call 的輸入和輸出
- 查看 Session 歷史，了解多輪對話的完整脈絡
- 切換不同的 Session 模擬不同使用者

> Dev UI 和 Telegram Bot 共用同一個 Agent 定義  
> 所以在 Dev UI 調好的行為，部署到 Telegram 時表現一致

---

## 3. 撰寫自訂 Tool

Tool 讓 Agent 具備「做事」的能力 — 查時間、呼叫 API、讀寫檔案  
沒有 Tool 的 Agent 只能聊天，有了 Tool 就能與真實世界互動

### 使用 `functiontool.New`

以 `get_current_time` 為例：

```go
import "google.golang.org/adk/tool/functiontool"

// 定義輸入參數（用 struct tag 描述 JSON schema）
type timeArgs struct {
    Timezone string `json:"timezone,omitempty" jsonschema:"IANA timezone such as Asia/Taipei. Defaults to Asia/Taipei if omitted."`
}

// 定義回傳結果
type timeResult struct {
    Now      string `json:"now"`
    Timezone string `json:"timezone"`
}

// 建立 Tool
currentTimeTool, err := functiontool.New(
    functiontool.Config{
        Name:        "get_current_time",
        Description: "Returns the current date and time.",
    },
    func(ctx tool.Context, args timeArgs) (timeResult, error) {
        tz := args.Timezone
        if tz == "" {
            tz = "Asia/Taipei"
        }
        loc, err := time.LoadLocation(tz)
        if err != nil {
            return timeResult{}, fmt.Errorf("invalid timezone %q: %w", tz, err)
        }
        now := time.Now().In(loc)
        return timeResult{
            Now:      now.Format(time.RFC3339),
            Timezone: tz,
        }, nil
    },
)
```

### Tool 設計要點

1. **Name + Description** — LLM 根據這兩個欄位決定「什麼時候該呼叫」這個 tool
2. **Args struct** — `json` tag 定義參數名稱，`jsonschema` tag 提供描述讓 LLM 理解
3. **回傳 struct** — 結果會自動序列化為 JSON 回傳給 LLM
4. **錯誤處理** — 回傳 `error` 時 LLM 會看到錯誤訊息並嘗試修正

> LLM 不會「看到」你的 Go 程式碼  
> 它只看到 Name、Description 和 JSON Schema  
> 所以 Description 寫得清楚比程式碼寫得漂亮更重要

---

## 4. 使用 MCP (Model Context Protocol)

MCP 讓你把外部服務的能力「掛載」到 Agent 上  
不需要自己寫 tool function，直接用現成的 MCP server

### 設定 MCP Server

本專案透過 `mcp.json` 設定要掛載的 MCP server，不需要改程式碼：

```json
{
  "mcpServers": {
    "chrome-devtools": {
      "command": "npx",
      "args": ["-y", "chrome-devtools-mcp@latest"]
    }
  }
}
```

要新增 MCP server？直接在 `mcpServers` 中加一筆即可：

```json
{
  "mcpServers": {
    "chrome-devtools": {
      "command": "npx",
      "args": ["-y", "chrome-devtools-mcp@latest"]
    },
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@anthropic/mcp-filesystem@latest", "/path/to/allowed/dir"]
    }
  }
}
```

程式啟動時會自動讀取 `mcp.json` 並載入所有 server：
然後掛到 Agent 上：

```go
mcpToolsets, err := loadMCPToolsets("mcp.json")

a, err := llmagent.New(llmagent.Config{
    // ...
    Toolsets: mcpToolsets,
})
```

---

## 5. 讓 Agent 自己寫 Prompt

這是本專案最有趣的部分 — Agent 透過 Tool 修改自己的 system prompt

### 流程

```
使用者第一次跟 Bot 對話
    ↓
Agent 讀取 agent.prompt（初始化 prompt）
    ↓
Agent 詢問使用者想給它取什麼名字
    ↓
使用者提供名字
    ↓
Agent 呼叫 set_agent_name tool
    ↓
Tool 產生 named.prompt 並寫入檔案
    ↓
下次啟動時，Agent 讀取 named.prompt 成為「已命名」的 Agent
```

### 初始化 Prompt

`agents/goost/agent.prompt`：

```handlebars
---
model: googleai/gemini-2.5-flash
---
{{role "system"}}

你是一個剛剛被啟動的全新 AI agent。

## 初始化流程

如果你還沒有名字（使用者尚未透過對話為你命名），請：
1. 友善地向使用者打招呼，告訴他們你是一個全新的 agent，還沒有名字
2. 請使用者為你取一個名字
3. 當使用者提供名字後，**必須**呼叫 `set_agent_name` 工具來儲存名字
4. 確認名字已設定，並以新名字自我介紹
```

### Prompt 切換邏輯

程式啟動時會檢查 `named.prompt` 是否存在：

```go
const promptDir = "agents/goost"
const namedPromptFile = "agents/goost/named.prompt"

promptName := "agent"
named := false
if _, err := os.Stat(namedPromptFile); err == nil {
    promptName = "named"
    named = true
}

modelName, instruction, err := loadPrompt(promptDir, promptName)
```

- `named.prompt` 不存在 → 載入 `agent.prompt`（初始化模式，提供 `set_agent_name` tool）
- `named.prompt` 存在 → 載入 `named.prompt`（正常模式，移除命名 tool）

### set_agent_name Tool

這個 tool 只在 Agent 還沒被命名時才存在：

```go
if !named {
    type nameArgs struct {
        Name string `json:"name" jsonschema:"The name to give this agent."`
    }
    type nameResult struct {
        Success bool   `json:"success"`
        Name    string `json:"name"`
        Message string `json:"message"`
    }
    setAgentNameTool, err := functiontool.New(
        functiontool.Config{
            Name:        "set_agent_name",
            Description: "Sets the agent's name by writing a named.prompt file.",
        },
        func(ctx tool.Context, args nameArgs) (nameResult, error) {
            name := strings.TrimSpace(args.Name)
            if name == "" {
                return nameResult{Success: false, Message: "Name cannot be empty."}, nil
            }

            prompt := fmt.Sprintf(`---
model: googleai/gemini-2.5-flash
---
{{role "system"}}

你是 %s，一個樂於助人的 AI 助手。

- 永遠用繁體中文（台灣）回覆
- 永遠以「%s」自稱
`, name, name)

            if err := os.WriteFile(namedPromptFile, []byte(prompt), 0644); err != nil {
                return nameResult{Success: false, Message: fmt.Sprintf("Failed: %v", err)}, nil
            }
            return nameResult{
                Success: true,
                Name:    name,
                Message: fmt.Sprintf("Agent name set to %q. Restart to apply.", name),
            }, nil
        },
    )
    tools = append(tools, setAgentNameTool)
}
```

---

## dotPrompt 簡介

本專案使用 [google/dotprompt](https://github.com/google/dotprompt) 管理 prompt  
好處是讓 prompt 與程式碼分離：

```go
import "github.com/google/dotprompt/go/dotprompt"

func loadPrompt(dir, name string) (modelName, instruction string, err error) {
    store, err := dotprompt.NewDirStore(dir)
    promptData, err := store.Load(name, dotprompt.LoadPromptOptions{})

    dp := dotprompt.NewDotprompt(nil)
    rendered, err := dp.Render(promptData.Source, &dotprompt.DataArgument{}, nil)

    modelName = rendered.Model

    // 收集 system role 的訊息作為 instruction
    var parts []string
    for _, msg := range rendered.Messages {
        if msg.Role == dotprompt.RoleSystem {
            for _, p := range msg.Content {
                if tp, ok := p.(*dotprompt.TextPart); ok {
                    parts = append(parts, tp.Text)
                }
            }
        }
    }
    instruction = strings.Join(parts, "\n")
    return
}
```

`.prompt` 檔案格式：
- **YAML frontmatter** — 定義 model 名稱等 metadata
- **Handlebars 模板** — `{{role "system"}}` 標記角色，支援變數插值
- **純文字** — prompt 內容就是一般文字，容易閱讀和版本控制
