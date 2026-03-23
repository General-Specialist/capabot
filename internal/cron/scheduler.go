package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/polymath/capabot/internal/agent"
	"github.com/polymath/capabot/internal/llm"
	"github.com/polymath/capabot/internal/memory"
	"github.com/polymath/capabot/internal/skill"
	"github.com/rs/zerolog"
)

// RunAgentFunc is the function signature for running an agent.
type RunAgentFunc func(ctx context.Context, sessionID string, messages []llm.ChatMessage, onEvent func(agent.AgentEvent)) (*agent.RunResult, error)

// Scheduler polls for due automations and runs them.
type Scheduler struct {
	store    *memory.Store
	skillReg *skill.Registry
	runAgent RunAgentFunc
	logger   zerolog.Logger
	triggerC chan int64 // manual trigger by automation ID
}

// NewScheduler creates a Scheduler.
func NewScheduler(store *memory.Store, skillReg *skill.Registry, runAgent RunAgentFunc, logger zerolog.Logger) *Scheduler {
	return &Scheduler{
		store:    store,
		skillReg: skillReg,
		runAgent: runAgent,
		logger:   logger.With().Str("component", "cron").Logger(),
		triggerC: make(chan int64, 16),
	}
}

// Start runs the scheduler loop until ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runDue(ctx)
		case id := <-s.triggerC:
			auto, err := s.store.GetAutomation(ctx, id)
			if err != nil {
				s.logger.Error().Err(err).Int64("id", id).Msg("manual trigger: automation not found")
				continue
			}
			go s.fire(ctx, auto, true)
		}
	}
}

// TriggerNow manually fires an automation immediately without affecting its schedule.
func (s *Scheduler) TriggerNow(id int64) {
	select {
	case s.triggerC <- id:
	default:
	}
}

func (s *Scheduler) runDue(ctx context.Context) {
	due, err := s.store.ListDueAutomations(ctx)
	if err != nil {
		s.logger.Error().Err(err).Msg("listing due automations")
		return
	}
	for _, auto := range due {
		go s.fire(ctx, auto, false)
	}
}

func (s *Scheduler) fire(ctx context.Context, auto memory.Automation, manual bool) {
	log := s.logger.With().Int64("automation_id", auto.ID).Str("name", auto.Name).Logger()
	log.Info().Bool("manual", manual).Msg("firing automation")

	runID, err := s.store.StartAutomationRun(ctx, auto.ID)
	if err != nil {
		log.Error().Err(err).Msg("starting run record")
		return
	}

	// Advance schedule before running so the next tick won't re-fire.
	if !manual && auto.RRule != "" {
		sched, parseErr := Parse(auto.RRule)
		if parseErr == nil {
			next := sched.Next(time.Now())
			_ = s.store.UpdateAutomationSchedule(ctx, auto.ID, time.Now(), next)
		}
	}

	// If a skill is configured, run it directly without the LLM.
	if auto.SkillName != "" {
		s.fireSkill(ctx, auto, runID, log)
		return
	}

	sessionID := fmt.Sprintf("auto-%d-%d", auto.ID, runID)
	messages := []llm.ChatMessage{{Role: "user", Content: auto.Prompt}}

	result, err := s.runAgent(ctx, sessionID, messages, nil)
	if err != nil {
		log.Error().Err(err).Msg("agent run failed")
		_ = s.store.FinishAutomationRun(ctx, runID, "error", "", err.Error())
		return
	}

	_ = s.store.FinishAutomationRun(ctx, runID, "success", result.Response, "")
	log.Info().Int("iterations", result.Iterations).Msg("automation complete")
}

// fireSkill runs a native or WASM skill directly, bypassing the LLM entirely.
func (s *Scheduler) fireSkill(ctx context.Context, auto memory.Automation, runID int64, log zerolog.Logger) {
	log.Info().Str("skill", auto.SkillName).Msg("running skill directly (no LLM)")

	// Build input JSON — pass the prompt as context if provided
	input := map[string]string{"prompt": auto.Prompt}
	inputJSON, _ := json.Marshal(input)

	// Try native skill first, then WASM
	if skillDir, ok := s.skillReg.NativePath(auto.SkillName); ok {
		exec, err := skill.NewNativeExecutor(ctx, skillDir)
		if err != nil {
			log.Error().Err(err).Msg("compiling native skill")
			_ = s.store.FinishAutomationRun(ctx, runID, "error", "", fmt.Sprintf("compile: %v", err))
			return
		}
		raw, err := exec.Execute(ctx, inputJSON)
		if err != nil {
			log.Error().Err(err).Msg("executing native skill")
			_ = s.store.FinishAutomationRun(ctx, runID, "error", "", fmt.Sprintf("execute: %v", err))
			return
		}
		result, err := skill.ParseWASMResult(raw)
		if err != nil {
			_ = s.store.FinishAutomationRun(ctx, runID, "error", "", fmt.Sprintf("parse: %v", err))
			return
		}
		status := "success"
		if result.IsError {
			status = "error"
		}
		_ = s.store.FinishAutomationRun(ctx, runID, status, result.Content, "")
		log.Info().Str("skill", auto.SkillName).Msg("skill automation complete")
		return
	}

	if wasmPath, ok := s.skillReg.WASMPath(auto.SkillName); ok {
		exec, err := skill.NewWASMExecutorFromFile(ctx, wasmPath)
		if err != nil {
			log.Error().Err(err).Msg("loading WASM skill")
			_ = s.store.FinishAutomationRun(ctx, runID, "error", "", fmt.Sprintf("wasm load: %v", err))
			return
		}
		defer exec.Close(ctx)
		raw, err := exec.Execute(ctx, inputJSON)
		if err != nil {
			log.Error().Err(err).Msg("executing WASM skill")
			_ = s.store.FinishAutomationRun(ctx, runID, "error", "", fmt.Sprintf("wasm execute: %v", err))
			return
		}
		result, err := skill.ParseWASMResult(raw)
		if err != nil {
			_ = s.store.FinishAutomationRun(ctx, runID, "error", "", fmt.Sprintf("wasm parse: %v", err))
			return
		}
		status := "success"
		if result.IsError {
			status = "error"
		}
		_ = s.store.FinishAutomationRun(ctx, runID, status, result.Content, "")
		log.Info().Str("skill", auto.SkillName).Msg("WASM skill automation complete")
		return
	}

	// Skill not found — fall back to agent
	log.Warn().Str("skill", auto.SkillName).Msg("skill not found, falling back to agent")
	_ = s.store.FinishAutomationRun(ctx, runID, "error", "", fmt.Sprintf("skill %q not found in registry", auto.SkillName))
}
