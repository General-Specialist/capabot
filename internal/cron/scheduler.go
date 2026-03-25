package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/polymath/gostaff/internal/agent"
	"github.com/polymath/gostaff/internal/llm"
	"github.com/polymath/gostaff/internal/memory"
	"github.com/polymath/gostaff/internal/skill"
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

	mu      sync.Mutex
	running map[int64]context.CancelFunc // runID → cancel
	subs    map[int64][]chan agent.AgentEvent // runID → subscribers
}

// NewScheduler creates a Scheduler.
func NewScheduler(store *memory.Store, skillReg *skill.Registry, runAgent RunAgentFunc, logger zerolog.Logger) *Scheduler {
	return &Scheduler{
		store:    store,
		skillReg: skillReg,
		runAgent: runAgent,
		logger:   logger.With().Str("component", "cron").Logger(),
		triggerC: make(chan int64, 16),
		running:  make(map[int64]context.CancelFunc),
		subs:     make(map[int64][]chan agent.AgentEvent),
	}
}

// StopRun cancels a running automation run. Returns true if the run was found and cancelled.
func (s *Scheduler) StopRun(runID int64) bool {
	s.mu.Lock()
	cancel, ok := s.running[runID]
	if ok {
		delete(s.running, runID)
	}
	s.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// Subscribe returns a channel that receives agent events for a running run.
// The channel is closed when the run finishes.
func (s *Scheduler) Subscribe(runID int64) <-chan agent.AgentEvent {
	ch := make(chan agent.AgentEvent, 64)
	s.mu.Lock()
	s.subs[runID] = append(s.subs[runID], ch)
	s.mu.Unlock()
	return ch
}

// broadcast sends an event to all subscribers for a run.
func (s *Scheduler) broadcast(runID int64, ev agent.AgentEvent) {
	s.mu.Lock()
	subs := s.subs[runID]
	s.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- ev:
		default: // drop if subscriber is slow
		}
	}
}

// closeSubs closes and removes all subscriber channels for a run.
func (s *Scheduler) closeSubs(runID int64) {
	s.mu.Lock()
	subs := s.subs[runID]
	delete(s.subs, runID)
	s.mu.Unlock()
	for _, ch := range subs {
		close(ch)
	}
}

// RunningRuns returns the IDs of currently running automation runs.
func (s *Scheduler) RunningRuns() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]int64, 0, len(s.running))
	for id := range s.running {
		ids = append(ids, id)
	}
	return ids
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

func (s *Scheduler) fire(parentCtx context.Context, auto memory.Automation, manual bool) {
	log := s.logger.With().Int64("automation_id", auto.ID).Str("name", auto.Name).Logger()
	log.Info().Bool("manual", manual).Msg("firing automation")

	runID, err := s.store.StartAutomationRun(parentCtx, auto.ID)
	if err != nil {
		log.Error().Err(err).Msg("starting run record")
		return
	}

	ctx, cancel := context.WithCancel(parentCtx)
	s.mu.Lock()
	s.running[runID] = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.running, runID)
		s.mu.Unlock()
		s.closeSubs(runID)
	}()

	// Advance schedule before running so the next tick won't re-fire.
	if !manual && auto.RRule != "" {
		sched, parseErr := Parse(auto.RRule)
		if parseErr == nil {
			next := sched.Next(time.Now())
			_ = s.store.UpdateAutomationSchedule(ctx, auto.ID, time.Now(), next)
		}
	}

	// Single executable skill + no prompt → run directly without LLM (zero tokens).
	// Otherwise the agent gets all skill_names registered as tools.
	if len(auto.SkillNames) == 1 && auto.Prompt == "" {
		s.fireSkill(ctx, auto, runID, log)
		return
	}

	sessionID := fmt.Sprintf("auto-%d-%d", auto.ID, runID)

	_ = s.store.UpsertSession(ctx, memory.Session{
		ID:       sessionID,
		TenantID: "default",
		Channel:  "automation",
		Title:    auto.Name,
		Metadata: "{}",
	})

	messages := []llm.ChatMessage{{Role: "user", Content: auto.Prompt}}

	result, err := s.runAgent(ctx, sessionID, messages, func(ev agent.AgentEvent) {
		s.broadcast(runID, ev)
	})
	if err != nil {
		log.Error().Err(err).Msg("agent run failed")
		if ctx.Err() == context.Canceled {
			_ = s.store.FinishAutomationRun(parentCtx, runID, "stopped", "", "stopped by user")
		} else {
			_ = s.store.FinishAutomationRun(parentCtx, runID, "error", "", err.Error())
		}
		return
	}

	_ = s.store.FinishAutomationRun(ctx, runID, "success", result.Response, "")
	log.Info().Int("iterations", result.Iterations).Msg("automation complete")
}

// fireSkill runs a native or plugin skill directly, bypassing the LLM entirely.
func (s *Scheduler) fireSkill(ctx context.Context, auto memory.Automation, runID int64, log zerolog.Logger) {
	skillName := auto.SkillNames[0]
	log.Info().Str("skill", skillName).Msg("running skill directly (no LLM)")

	// Build input JSON — pass the prompt as context if provided
	input := map[string]string{"prompt": auto.Prompt}
	inputJSON, _ := json.Marshal(input)

	// Try native skill first, then plugin
	if skillDir, ok := s.skillReg.NativePath(skillName); ok {
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
		result, err := skill.ParseSkillResult(raw)
		if err != nil {
			_ = s.store.FinishAutomationRun(ctx, runID, "error", "", fmt.Sprintf("parse: %v", err))
			return
		}
		status := "success"
		if result.IsError {
			status = "error"
		}
		_ = s.store.FinishAutomationRun(ctx, runID, status, result.Content, "")
		log.Info().Str("skill", skillName).Msg("skill automation complete")
		return
	}

	log.Warn().Str("skill", skillName).Msg("skill has no executable — needs a prompt to run via agent")
	_ = s.store.FinishAutomationRun(ctx, runID, "error", "", fmt.Sprintf("skill %q has no executable; add a prompt to run it via the agent", skillName))
}
