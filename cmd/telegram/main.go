// Package main implements a Telegram Bot backed by an ADK Go agent.
//
// Usage:
//
//	# Telegram bot mode
//	TELEGRAM_BOT_TOKEN=<token> GOOGLE_GENAI_API_KEY=<key> go run ./cmd/telegram
//
//	# ADK Dev UI mode
//	GOOGLE_GENAI_API_KEY=<key> go run ./cmd/telegram web --port 9090
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/google/dotprompt/go/dotprompt"
	"github.com/joho/godotenv"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/cmd/launcher"
	"google.golang.org/adk/cmd/launcher/full"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	dbsession "google.golang.org/adk/session/database"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/adk/tool/mcptoolset"
	"google.golang.org/genai"
)

// mcpConfig represents the structure of mcp.json.
type mcpConfig struct {
	MCPServers map[string]mcpServerConfig `json:"mcpServers"`
}

type mcpServerConfig struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// loadMCPToolsets reads mcp.json and creates a toolset for each server entry.
func loadMCPToolsets(path string) ([]tool.Toolset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Info("no mcp.json found, skipping MCP toolsets")
			return nil, nil
		}
		return nil, fmt.Errorf("reading mcp config: %w", err)
	}

	var cfg mcpConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing mcp config: %w", err)
	}

	var toolsets []tool.Toolset
	for name, server := range cfg.MCPServers {
		ts, err := mcptoolset.New(mcptoolset.Config{
			Transport: &mcp.CommandTransport{
				Command: exec.Command(server.Command, server.Args...),
			},
		})
		if err != nil {
			return nil, fmt.Errorf("creating MCP toolset %q: %w", name, err)
		}
		slog.Info("loaded MCP server", "name", name, "command", server.Command)
		toolsets = append(toolsets, ts)
	}
	return toolsets, nil
}

// loadPrompt loads and parses a .prompt file via dotprompt DirStore.
// Returns the model name from frontmatter and the rendered system instruction.
func loadPrompt(dir, name string) (modelName, instruction string, err error) {
	store, err := dotprompt.NewDirStore(dir)
	if err != nil {
		return "", "", fmt.Errorf("creating prompt store: %w", err)
	}

	promptData, err := store.Load(name, dotprompt.LoadPromptOptions{})
	if err != nil {
		return "", "", fmt.Errorf("loading prompt %q: %w", name, err)
	}

	dp := dotprompt.NewDotprompt(nil)
	rendered, err := dp.Render(promptData.Source, &dotprompt.DataArgument{}, nil)
	if err != nil {
		return "", "", fmt.Errorf("rendering prompt: %w", err)
	}

	modelName = rendered.Model

	// Collect system messages as the instruction.
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

	return modelName, instruction, nil
}

// buildAgent constructs the ADK agent with all tools configured.
func buildAgent(ctx context.Context) (agent.Agent, error) {
	apiKey := os.Getenv("GOOGLE_GENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("GOOGLE_GENAI_API_KEY is not set")
	}

	backend := genai.BackendGeminiAPI
	if strings.EqualFold(os.Getenv("GOOGLE_GENAI_USE_VERTEXAI"), "true") {
		backend = genai.BackendVertexAI
	}

	// --- Load system prompt via dotprompt ---
	// If "named" prompt exists, the agent has already been named — use it.
	// Otherwise, use "agent" prompt (initialization flow).
	const promptDir = "agents/goost"
	const namedPromptFile = "agents/goost/named.prompt"

	promptName := "agent"
	named := false
	if _, err := os.Stat(namedPromptFile); err == nil {
		promptName = "named"
		named = true
	}

	modelName, instruction, err := loadPrompt(promptDir, promptName)
	if err != nil {
		return nil, err
	}
	if i := strings.Index(modelName, "/"); i >= 0 {
		modelName = modelName[i+1:]
	}
	if modelName == "" {
		modelName = "gemini-2.5-flash"
	}

	// --- Custom tools ---
	var tools []tool.Tool

	// set_agent_name: only available when the agent has not been named yet.
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
				Description: "Sets the agent's name by writing a named.prompt file. Call this when the user provides a name for the agent.",
			},
			func(ctx tool.Context, args nameArgs) (nameResult, error) {
				name := strings.TrimSpace(args.Name)
				if name == "" {
					return nameResult{Success: false, Message: "Name cannot be empty."}, nil
				}

				// TODO: Build the named prompt content here.
				// This is where YOU decide what the "named agent" prompt looks like.
				// For now, using fmt.Sprintf as a placeholder — replace with your ideal template.
				prompt := fmt.Sprintf(`---
model: googleai/gemini-3-flash-preview
---
{{role "system"}}

你是 %s，一個樂於助人的 AI 助手。

- 永遠用繁體中文（台灣）回覆
- 永遠以「%s」自稱
`, name, name)

				if err := os.WriteFile(namedPromptFile, []byte(prompt), 0644); err != nil {
					return nameResult{Success: false, Message: fmt.Sprintf("Failed to write prompt file: %v", err)}, nil
				}
				slog.Info("named.prompt created", "name", name)
				return nameResult{
					Success: true,
					Name:    name,
					Message: fmt.Sprintf("Agent name has been permanently set to %q. Restart to apply.", name),
				}, nil
			},
		)
		if err != nil {
			return nil, fmt.Errorf("creating set_agent_name tool: %w", err)
		}
		tools = append(tools, setAgentNameTool)
	}

	// get_current_time: provides the LLM with accurate time information.
	type timeArgs struct {
		Timezone string `json:"timezone,omitempty" jsonschema:"IANA timezone such as Asia/Taipei. Defaults to Asia/Taipei if omitted."`
	}
	type timeResult struct {
		Now      string `json:"now"`
		Timezone string `json:"timezone"`
	}
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
	if err != nil {
		return nil, fmt.Errorf("creating current_time tool: %w", err)
	}

	// --- MCP: load from mcp.json ---
	mcpToolsets, err := loadMCPToolsets("mcp.json")
	if err != nil {
		return nil, err
	}

	// --- Create Gemini model ---
	model, err := gemini.NewModel(ctx, modelName, &genai.ClientConfig{
		Backend: backend,
		APIKey:  apiKey,
	})
	if err != nil {
		return nil, fmt.Errorf("creating gemini model: %w", err)
	}

	// --- Assemble agent ---
	a, err := llmagent.New(llmagent.Config{
		Name:        "agent",
		Model:       model,
		Description: "A new agent that can be named and configured through conversation.",
		Instruction: instruction,
		Tools:       append(tools, currentTimeTool),
		Toolsets:    mcpToolsets,
	})
	if err != nil {
		return nil, fmt.Errorf("creating agent: %w", err)
	}

	slog.Info("agent ready", "model", modelName)
	return a, nil
}

// extractReplyText walks runner events and collects text parts from
// non-user authors, returning the concatenated reply.
func extractReplyText(events func(func(*session.Event, error) bool)) (string, error) {
	var reply strings.Builder
	for event, err := range events {
		if err != nil {
			return "", err
		}
		if event.Author == "user" || event.Content == nil {
			continue
		}
		for _, part := range event.Content.Parts {
			if part.Text != "" {
				reply.WriteString(part.Text)
			}
		}
	}
	return reply.String(), nil
}

// runTelegram starts the Telegram bot handler.
func runTelegram(ctx context.Context, a agent.Agent, sessService session.Service) error {
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	if botToken == "" {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN is not set")
	}

	r, err := runner.New(runner.Config{
		AppName:           "telegram-bot",
		Agent:             a,
		SessionService:    sessService,
		AutoCreateSession: true,
	})
	if err != nil {
		return fmt.Errorf("creating runner: %w", err)
	}

	handler := func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if update.Message == nil || update.Message.Text == "" || update.Message.From == nil {
			return
		}

		chatID := update.Message.Chat.ID
		userID := strconv.FormatInt(update.Message.From.ID, 10)
		sessionID := strconv.FormatInt(chatID, 10)

		userMsg := genai.NewContentFromText(update.Message.Text, genai.RoleUser)

		reply, err := extractReplyText(r.Run(ctx, userID, sessionID, userMsg, agent.RunConfig{}))
		if err != nil {
			slog.Error("runner error", "error", err)
			reply = "(internal error)"
		}
		if reply == "" {
			reply = "(no response)"
		}

		if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   reply,
		}); err != nil {
			slog.Error("failed to send reply", "error", err)
		}
	}

	b, err := bot.New(botToken, bot.WithDefaultHandler(handler))
	if err != nil {
		return fmt.Errorf("creating telegram bot: %w", err)
	}

	slog.Info("telegram bot started")
	b.Start(ctx)
	return nil
}

func run() error {
	_ = godotenv.Load()

	ctx := context.Background()

	a, err := buildAgent(ctx)
	if err != nil {
		return err
	}

	// SQLite-based session service — persists conversations to a local file.
	sessService, err := dbsession.NewSessionService(
		sqlite.Open("sessions.db"),
		&gorm.Config{},
	)
	if err != nil {
		return fmt.Errorf("creating session service: %w", err)
	}
	if err := dbsession.AutoMigrate(sessService); err != nil {
		return fmt.Errorf("migrating session schema: %w", err)
	}

	// If subcommand is "web" or "console", delegate to ADK launcher.
	// Otherwise, start the Telegram bot.
	if len(os.Args) > 1 {
		l := full.NewLauncher()
		return l.Execute(ctx, &launcher.Config{
			AgentLoader:    agent.NewSingleLoader(a),
			SessionService: sessService,
		}, os.Args[1:])
	}

	return runTelegram(ctx, a, sessService)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
