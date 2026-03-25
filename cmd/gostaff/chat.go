package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/polymath/gostaff/internal/agent"
	applog "github.com/polymath/gostaff/internal/log"
	"github.com/polymath/gostaff/internal/llm"
)

func runChat(configPath string) error {
	// 1. Load config
	cfg, err := loadOrDefault(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// 2. Setup logger (error-only to stderr, quiet for chat UX)
	logger := applog.New("error", false)

	// 3. Initialize LLM providers
	ctx := context.Background()
	router, err := initRouter(ctx, cfg)
	if err != nil {
		return fmt.Errorf("initializing LLM providers: %w", err)
	}

	// 4. Initialize tool registry (no store in CLI chat mode)
	toolRegistry, _ := initToolRegistry(cfg, nil)

	// 5. Initialize skill registry
	_ = initSkillRegistry(cfg)

	// 6. Build default agent
	agentCfg := agent.AgentConfig{
		ID:            "default",
		Model:         "",
		SystemPrompt:  "You are a helpful AI assistant.",
		MaxIterations: cfg.Agent.MaxIterations,
		MaxTokens:     4096,
	}
	ctxMgrCfg := agent.ContextConfig{
		ContextWindow:       200000,
		BudgetPct:           cfg.Agent.ContextBudgetPct,
		MaxToolOutputTokens: cfg.Agent.MaxToolOutputTokens,
	}
	ctxMgr := agent.NewContextManager(ctxMgrCfg)
	a := agent.New(agentCfg, router, toolRegistry, ctxMgr, logger)

	// 7. Session ID
	sessionID := fmt.Sprintf("cli-%d", time.Now().Unix())

	// 8. Interactive loop
	fmt.Println("GoStaff Chat — type 'exit' to quit")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	var history []llm.ChatMessage

	for {
		fmt.Print("You: ")

		if !scanner.Scan() {
			// EOF
			fmt.Println()
			break
		}

		line := scanner.Text()
		if line == "exit" || line == "quit" {
			break
		}
		if line == "" {
			continue
		}

		// Append user message to history
		history = append(history, llm.ChatMessage{
			Role:    "user",
			Content: line,
		})

		result, err := a.Run(ctx, sessionID, history)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}

		fmt.Printf("Bot: %s\n\n", result.Response)

		// Append assistant response to history for multi-turn context
		history = append(history, llm.ChatMessage{
			Role:    "assistant",
			Content: result.Response,
		})
	}

	return nil
}
