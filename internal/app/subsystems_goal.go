// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"fmt"
	goaltools "github.com/pijalu/goa/tools/goal"
	"path/filepath"
	"sync"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/core/swarm"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/provider"
	"github.com/pijalu/goa/tools"
)

func configureGoalMode(cfg *config.Config, projectDir string, manager *core.GoalManager, publisher *goalEventPublisher, providerMgr *provider.ProviderManager) {
	// Done-gate (goals.done_gate): how strictly model-initiated completion is
	// checked before the goal may close. Invalid values are rejected by config
	// validation; fall back to the default defensively.
	if gate, ok := goal.ParseDoneGate(cfg.Goals.DoneGate); ok {
		manager.Mode.SetDoneGate(gate)
	} else {
		manager.Mode.SetDoneGate(goal.DefaultDoneGate)
	}
	// Machine verification (goals.verify_commands): exec the recorded verify
	// command in the project dir at completion time, bounded by the
	// configurable goals.verify_timeout (default 2m).
	manager.Mode.SetVerifier(newExecCommandVerifier(projectDir, cfg.Goals.VerifyTimeoutOr(defaultGoalVerifyTimeout)), cfg.Goals.VerifyCommandsEnabled())
	// Escalation bound (goals.max_verify_failures): config -1 = no cap maps
	// to mode 0; negative values are clamped to 0 defensively.
	manager.Mode.SetMaxVerifyFailures(max(cfg.Goals.MaxVerifyFailures, 0))
	// Default turn budget (goals.default_turn_budget): -1 = unlimited maps
	// to mode 0 (no implicit budget).
	manager.Mode.SetDefaultTurnBudget(max(cfg.Goals.DefaultTurnBudget, 0))
	// Independent completion judge (goals.judge): off by default; resolution
	// failure is fail-open (no judge) and reported on the bus.
	if judge, err := newGoalJudge(cfg.Goals.Judge, providerMgr); err != nil {
		publisher.PublishSystemMessage("goal judge disabled: " + err.Error())
	} else if judge != nil {
		manager.Mode.SetJudge(judge)
	}
}

func initGoalSystem(cfg *config.Config, projectDir string, eventBus *event.Bus, agentMgr *core.AgentManager, swarmState *swarm.State, providerMgr *provider.ProviderManager) (*core.GoalManager, *core.GoalDriver) {
	reminderFn := func(text string) {
		if agentMgr != nil {
			_ = agentMgr.InjectSystemMessage(text)
		}
	}
	publisher := &goalEventPublisher{bus: eventBus}
	manager := core.NewGoalManager(filepath.Join(projectDir, ".goa"), core.GoalDependencies{
		Publisher:  publisher,
		Telemetry:  nil,
		ReminderFn: reminderFn,
	})
	configureGoalMode(cfg, projectDir, manager, publisher, providerMgr)
	// Chain goal + swarm reminders into a single provider so the swarm
	// enter-reminder is prepended to the system prompt every turn while swarm
	// mode is active under a manual/task trigger.
	agentMgr.SetGoalStateProvider(&core.ReminderProvider{
		Sources: []core.GoalReminderSource{
			&core.GoalInjector{Mode: manager.Mode},
			core.SwarmReminder{State: swarmState},
		},
	})
	driver := &core.GoalDriver{
		Mode:  manager.Mode,
		Agent: &agentManagerRunner{agentMgr: agentMgr},
	}
	// Stall watchdog (goals.stall_turns): challenge unmanaged goals whose
	// progress fingerprint stops changing. -1/0 = disabled.
	if cfg.Goals.StallTurns > 0 {
		driver.Probe = newGoalStallProbe(projectDir, manager.Mode)
		driver.Remind = reminderFn
		driver.StallTurns = cfg.Goals.StallTurns
	}
	// Wire goal token tracking: each token stats event reports cumulative
	// tokens for the current turn; compute the delta and accrue to goal.
	// Token totals are per-turn (reset between turns), so a smaller total
	// signals a new turn — reset the accumulator.
	var lastGoalTokens int
	agentMgr.SetGoalTokenRecorder(func(total int) {
		if total < lastGoalTokens {
			lastGoalTokens = 0 // new turn started
		}
		if total > lastGoalTokens {
			delta := total - lastGoalTokens
			if _, err := manager.Mode.RecordTokenUsage(delta); err != nil {
				agentMgr.EmitEvent("Failed to record goal tokens: " + err.Error())
			}
			lastGoalTokens = total
		}
	})
	agentMgr.SetPostTurnHook(func() {
		if active := manager.Mode.GetActiveGoal(); active != nil {
			_ = driver.Drive(context.Background())
		}
	})
	// End-of-turn swarm auto-exit (kimi-code parity): task/tool triggers
	// deactivate after the turn; the manual toggle persists. On auto-exit,
	// inject the exit reminder so the model drops the swarm workflow.
	agentMgr.SetPostTurnHook(func() {
		if swarmState.MaybeAutoExit() {
			_ = agentMgr.InjectSystemMessage(swarm.ExitReminder())
		}
	})
	return manager, driver
}

// wireDreamScheduler registers a post-turn hook that records completed
// sessions for automatic memory consolidation. The scheduler itself is
// started during subsystem assembly once the event bus is available.
func wireDreamScheduler(agentMgr *core.AgentManager, scheduler *dreamScheduler) {
	if scheduler == nil {
		return
	}
	agentMgr.SetPostTurnHook(func() {
		scheduler.RecordSession()
	})
}

// goalEventPublisher delivers goal state changes to the app's Agent bus.
// Delivery is lossless and ordered (Issue 1): a non-blocking send
// used to silently drop updates when the bus was full — exactly the
// mid-turn situation where a goal create/resume/complete happens — leaving
// the goal bubble hidden (create dropped) or stale (clear dropped). When
// the bus is full, updates queue in publish order and a single drain
// goroutine delivers them as room frees up.
type goalEventPublisher struct {
	bus *event.Bus

	mu       sync.Mutex
	queue    []event.AgentEvent
	draining bool
}

func (p *goalEventPublisher) Publish(snapshot *goal.GoalSnapshot, change *goal.GoalChange) {
	if p.bus == nil {
		return
	}
	ev := event.AgentEvent{GoalUpdate: &event.GoalUpdate{Snapshot: snapshot, Change: change}}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.queue) > 0 {
		// A drain is already pending: keep publish order by queueing.
		p.queue = append(p.queue, ev)
		return
	}
	select {
	case p.bus.Agent <- ev:
		return
	default:
	}
	p.queue = append(p.queue, ev)
	if !p.draining {
		p.draining = true
		go p.drain()
	}
}

// drain delivers queued updates in publish order, blocking on the bus until
// the consumer catches up. At most one drain goroutine runs at a time.
func (p *goalEventPublisher) drain() {
	for {
		p.mu.Lock()
		if len(p.queue) == 0 {
			p.draining = false
			p.mu.Unlock()
			return
		}
		ev := p.queue[0]
		p.queue = p.queue[1:]
		p.mu.Unlock()
		p.bus.Agent <- ev
	}
}

// PublishSystemMessage surfaces a goal-subsystem notice (e.g. a disabled
// judge) as a transient flash. Best-effort: dropped when the bus is full
// or nil.
func (p *goalEventPublisher) PublishSystemMessage(text string) {
	if p.bus == nil {
		return
	}
	select {
	case p.bus.Chat <- event.ChatEvent{Flash: &event.Flash{Text: text}}:
	default:
	}
}

// agentManagerRunner adapts AgentManager to the core.AgentRunner interface
// used by GoalDriver. It runs turns against the currently active agent.
type agentManagerRunner struct {
	agentMgr *core.AgentManager
}

func (r *agentManagerRunner) Run(ctx context.Context, input string) error {
	// Never run a goal turn while a user turn owns the agent: agent.Run's
	// queue-on-busy semantics would return instantly and the drive loop
	// would hot-spin, queueing hundreds of phantom continuation prompts
	// (Issue 7). The in-flight turn's post-turn hook re-starts the
	// drive once the agent is idle.
	if r.agentMgr.IsRunning() {
		return core.ErrAgentBusy
	}
	agent := r.agentMgr.CurrentAgent()
	if agent == nil {
		return fmt.Errorf("no active agent session")
	}
	err := agent.Run(ctx, input)
	// Goal continuation turns bypass runAgentTurn, so its end-of-turn
	// leftover-steering flush never runs for them: steering typed during this
	// turn's final round would stay queued (bubble stuck) until some unrelated
	// future turn. Dispatch it as the follow-up user turn here; the drive
	// loop's next iteration then sees the agent busy (ErrAgentBusy) and the
	// steering turn's post-turn hook re-starts the drive.
	r.agentMgr.DispatchPendingSteering()
	return err
}

// ResetLoopStop clears the runaway-loop latch on the active agent. The goal
// driver calls this (via optional interface) when resuming a goal paused by
// the loop guardrail, pairing the latch reset with its varied recovery
// prompt (runaway-loop bricking).
func (r *agentManagerRunner) ResetLoopStop() {
	if agent := r.agentMgr.CurrentAgent(); agent != nil {
		agent.ResetLoopStop()
	}
}

// RunFresh implements core.FreshAgentRunner for goals carrying the
// clean-context flag. On the first continuation turn (begin=true) it clears the
// agent's live context to a clean one (system prompt only) and renders a
// visible boundary marker so the user can follow where the prior context ended
// and the fresh goal context began. Subsequent turns (begin=false) reuse the
// already-clean context. The prior conversation is preserved in the durable
// transcript (session event log); it is intentionally not restored into the
// live context mid-session, matching "new agent / clean context" semantics —
// the goal carries only its objective forward.
func (r *agentManagerRunner) RunFresh(ctx context.Context, input string, begin bool) error {
	// Same busy guard as Run — a fresh-context goal must never queue-storm
	// either (Issue 7).
	if r.agentMgr.IsRunning() {
		return core.ErrAgentBusy
	}
	agent := r.agentMgr.CurrentAgent()
	if agent == nil {
		return fmt.Errorf("no active agent session")
	}
	if begin {
		agent.SetHistory(nil) // system prompt is re-prepended by SetHistory
		// Rotate the conversation id so the clean context also gets a fresh
		// provider cache key (prompt_cache_key / previous_response_id /
		// session-affinity) — a clean context pinned to the old SessionID would
		// keep reading the prior conversation's cache (Issue 8).
		r.agentMgr.ResetConversationID()
		r.agentMgr.InjectSystemMessage("⟡ Context reset: this goal is running on a clean context. The prior conversation is preserved in the transcript but is not sent to the agent for this goal.")
		// Re-arm the cache-bust detector for the new conversation: its cold
		// start (zero or tiny cache reads on the fresh provider cache key)
		// must not count as a bust against the prior conversation's
		// established cache (fresh-context goal start counted as a
		// cache miss).
		agent.EmitContextReset()
	}
	err := agent.Run(ctx, input)
	// Same leftover-steering dispatch as Run (see there): fresh-context goal
	// turns bypass runAgentTurn's flush just like ordinary goal turns.
	r.agentMgr.DispatchPendingSteering()
	return err
}

func registerGoalTools(toolRegistry *tools.ToolRegistry, manager *core.GoalManager, createFlagOn bool, autoUnblock func() bool, freshContextDefault func() bool, verifyTimeout func() time.Duration) {
	toolRegistry.Register(newGoalTool(manager, createFlagOn, autoUnblock, freshContextDefault, verifyTimeout))
}

// newGoalTool builds the single agent-facing goal tool bound to the manager's
// GoalMode. Extracted so both the startup registration path and the runtime
// /tools:goal:on factory (makeToolFactory) construct it identically.
// autoUnblock gates the auto-spawning of an unblocking investigation goal when
// the model blocks a goal with justification (goals.auto_unblock; nil = on).
// verifyTimeout feeds the live display of the verify-command bound at goal
// completion (goals.verify_timeout; nil = default 2m) — Bug A.
func newGoalTool(manager *core.GoalManager, createFlagOn bool, autoUnblock func() bool, freshContextDefault func() bool, verifyTimeout func() time.Duration) agentic.Tool {
	// Autonomous `create` is allowed when the feature flag is on, or whenever a
	// goal exists (S2: all goal actions work during a goal). Existence
	// — not just active status — matters: a paused/blocked goal still means
	// "during a goal", and the tool queues behind it ("Goal management
	// tool issue").
	createAllowed := func() bool {
		return createFlagOn || manager.Mode.GetGoal().Goal != nil
	}
	tool := tools.NewGoalTools(manager.Mode, createAllowed)[0]
	// Wire the durable goal queue so the tool manages goals as a todo-like
	// list: create appends by default when a goal is active, and list/cancel/
	// reorder operate over the active goal + the queue.
	if gt, ok := tool.(*goaltools.GoalTool); ok {
		gt.Queue = manager.Queue
		gt.AutoUnblock = autoUnblock
		gt.FreshContextDefault = freshContextDefault
		gt.VerifyTimeout = verifyTimeout
	}
	return tool
}
