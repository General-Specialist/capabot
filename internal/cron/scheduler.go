package cron

import (
	"context"
	"fmt"
	"time"

	"github.com/polymath/capabot/internal/agent"
	"github.com/polymath/capabot/internal/llm"
	"github.com/polymath/capabot/internal/memory"
	"github.com/rs/zerolog"
)

// RunAgentFunc is the function signature for running an agent.
type RunAgentFunc func(ctx context.Context, sessionID string, messages []llm.ChatMessage, onEvent func(agent.AgentEvent)) (*agent.RunResult, error)

// Scheduler polls for due automations and runs them.
type Scheduler struct {
	store    *memory.Store
	runAgent RunAgentFunc
	logger   zerolog.Logger
	triggerC chan int64 // manual trigger by automation ID
}

// NewScheduler creates a Scheduler.
func NewScheduler(store *memory.Store, runAgent RunAgentFunc, logger zerolog.Logger) *Scheduler {
	return &Scheduler{
		store:    store,
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
	if !manual {
		sched, parseErr := Parse(auto.Cron)
		if parseErr == nil {
			next := sched.Next(time.Now())
			_ = s.store.UpdateAutomationSchedule(ctx, auto.ID, time.Now(), next)
		}
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
