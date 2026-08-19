// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/internal/hooks"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/prompts"
)

// TurnTokenUsage holds per-turn token usage breakdown.
type TurnTokenUsage struct {
	PromptN         int     // input tokens
	PredictedN      int     // output tokens
	CacheRead       int     // cache read tokens
	CacheWrite      int     // cache creation tokens
	SpeedTokPerSec  float64 // output token/s
	CostUSD         float64 // estimated cost in USD
	ContextEstimate int     // estimated context usage at turn end
	ContextMax      int     // max context window
}

// TurnRecord holds data for one agent turn.
type TurnRecord struct {
	Number             int
	RequestJSON        string // serialized conversation history sent to the LLM
	ResponseJSON       string // serialized assistant response content
	TokensUsed         int
	TokenUsage         TurnTokenUsage // detailed token breakdown
	Timing             TurnTiming
	ToolCalls          []TurnToolCall   // tool calls made during this turn
	ToolResults        []TurnToolResult // tool results received during this turn
	UserInput          string           // the user message that started this turn
	Thinking           []string         // thinking blocks emitted by the model
	AssistantResponses []string         // assistant content blocks emitted by the model
	// AgentRole identifies which agent produced the turn: "" or "main" for
	// the primary session agent, otherwise the multiagent role (companion,
	// workflow stage, team member). Drives the per-agent /stats:cache
	// sections.
	AgentRole string
	// GoalID is the active goal at finalize time ("" = no goal), so the
	// cache view can group turns per goal as well as per agent.
	GoalID string
}

// TurnToolCall records a tool call made during a turn.
type TurnToolCall struct {
	Name   string
	Input  string
	CallID string
}

// TurnToolResult records a tool result received during a turn.
type TurnToolResult struct {
	CallID string
	Name   string
	Result string
}

// TurnTiming holds timing metrics for a turn.
type TurnTiming struct {
	TTFT   float64 // time to first token in seconds
	Total  float64 // total turn duration in seconds
	Phases map[string]float64
}

// agentRunner is the subset of *agentic.Agent used by runAgentTurn. Using an
// interface makes the turn runner testable and keeps AgentManager decoupled
// from the full agent surface.
type agentRunner interface {
	Run(ctx context.Context, input string) error
	RunWithImages(ctx context.Context, input string, images []string) error
}

// AgentManager manages the lifecycle of the LLM agent session. It is a thin
// facade over focused collaborators: TurnRecorder, ModeManager,
// CompanionCoordinator, AgentDrivenGate, and SteeringQueue.
type AgentManager struct {
	cfg            *config.Config
	activeAgent    *agentic.Agent
	events         chan agentic.OutputEvent
	sessionStore   *SessionStore      // event recording store
	stateStore     *StateStore        // persisted mode state store
	configSaver    config.ConfigSaver // persists per-model thinking level to the home config
	loopDetector   *LoopDetector
	eventsOut      *event.Bus
	logger         *agentic.Logger
	mu             sync.Mutex
	cancel         context.CancelFunc
	cancelGen      int
	running        bool
	lastUserInput  string
	systemPrompt   string
	agentBus       *agentic.AgentBus
	mainConnector  *agentic.CommConnector
	foregroundOrch *multiagent.ForegroundOrchestrator
	// forwardInternalEvents controls whether OnEvent also writes to the
	// internal am.events channel. The TUI consumes events from eventsOut.Agent
	// and never reads am.events, so leaving this false prevents the agent from
	// blocking once the 100-slot internal buffer fills. Headless/ACP consumers
	// that call Events() must set this to true.
	forwardInternalEvents bool

	turnRecorder         *TurnRecorder
	modeMgr              *ModeManager
	modeRegistry         *ModeRegistry
	pendingMajor         *internal.MajorMode
	pendingThinkingLevel *string
	companion            *CompanionCoordinator
	agentDriven          *AgentDrivenGate
	steering             *SteeringQueue
	pendingSteering      string // steering text saved during finalizeTurn, dispatched after am.running=false
	companionBuf         strings.Builder
	goalStateProvider    agentic.GoalStateProvider
	stickyProvider       agentic.StickyProvider
	preTurnProvider      agentic.PreTurnProvider
	spillPolicyFactory   func(sessionID string) agentic.SpillPolicy
	postTurnHook         func()
	lifecycleRegistry    LifecycleRegistry
	projectDir           string
	confirmTool          func(ctx context.Context, toolName, input string) (bool, error)

	// goalTokenRecorder is called with the latest total token count for
	// the active agent turn. Used by the goal system to track token budget.
	goalTokenRecorder func(totalTokens int)

	// activeGoalID returns the ID of the goal active at turn finalize time
	// ("" = none). Tags the main agent's TurnRecord so the cache view can
	// group turns per goal. Nil = untagged.
	activeGoalID func() string

	contextWindowRefresher func() int
	contextWindowRefreshed bool
	baseSystemPrompt       string
	companionReviewEnabled bool
	companionReviewSet     bool
	hookEngine             hooks.AgentHookEngine

	// disableToolBudget is a session-level flag that disables the per-turn
	// tool-call budget check. When set, the agent allows unlimited tool calls
	// per turn. Not persisted — resets on restart.
	disableToolBudget bool

	// loopStopReason is set when the loop detector cancels the turn so that
	// executeRunner can emit a clear EventEnd instead of the generic
	// "Generation stopped by user." cancellation message.
	loopStopReason string

	// eventFwd decouples the streaming goroutine's event emission from the
	// bounded app event bus (see eventForwarder). nil when eventsOut is nil.
	eventFwd *eventForwarder

	// pendingInputHistory holds input history extracted from a restored
	// session, waiting to be applied to the editor by the app layer.
	pendingInputHistory []string
}

// NewAgentManager creates a new agent manager.
func NewAgentManager(cfg *config.Config, sessionStore *SessionStore, loopDetector *LoopDetector, sessionState *SessionState, eventsOut *event.Bus, projectDir string) *AgentManager {
	agentDriven := NewAgentDrivenGate()
	am := &AgentManager{
		cfg:          cfg,
		events:       make(chan agentic.OutputEvent, 100),
		sessionStore: sessionStore,
		loopDetector: loopDetector,
		eventsOut:    eventsOut,
		agentBus:     agentic.NewAgentBus(),
		agentDriven:  agentDriven,
		turnRecorder: NewTurnRecorder(),
		modeMgr:      NewModeManager(sessionState, agentDriven),
		companion:    NewCompanionCoordinator(),
		steering:     NewSteeringQueue(),
		projectDir:   projectDir,
	}
	if eventsOut != nil {
		am.eventFwd = newEventForwarder(eventsOut.Agent)
	}

	return am
}

// StartSession creates a new agent session.
func (am *AgentManager) StartSession(mdl agenticprovider.Model, opts agenticprovider.StreamOptions, systemPrompt string, tools []agentic.Tool, cfg *config.Config) (<-chan agentic.OutputEvent, error) {
	am.mu.Lock()
	defer am.mu.Unlock()

	if am.activeAgent != nil {
		return nil, fmt.Errorf("session already active")
	}

	tools = am.toolsWithBus(tools)
	am.baseSystemPrompt = systemPrompt
	finalPrompt := am.augmentSystemPrompt(systemPrompt)
	if am.sessionStore != nil {
		sessionID := am.sessionStore.StartSession()
		if sessionID != "" {
			opts.SessionID = sessionID
			am.fireSessionStart(sessionID)
		}
	}

	agent := agentic.NewAgent(am.buildAgenticConfig(mdl, opts, finalPrompt, tools, cfg))

	am.systemPrompt = finalPrompt
	agent.AddObserver(am)
	// Wire mid-turn steering (steering-lateness; pi parity): the agent
	// polls this queue between stream rounds and weaves steering into the
	// current turn instead of delivering it as a late, separate turn.
	if am.steering != nil {
		agent.SetSteeringSource(steeringSourceAdapter{am.steering})
	}

	am.wireMainAgent(agent)

	am.activeAgent = agent
	am.contextWindowRefreshed = false
	am.dispatchLifecycle("start", map[string]any{
		"model":    mdl.ID,
		"provider": am.cfg.ActiveProvider,
	})
	return am.events, nil
}

func (am *AgentManager) toolsWithBus(tools []agentic.Tool) []agentic.Tool {
	if am.agentBus == nil {
		return tools
	}
	result := make([]agentic.Tool, len(tools), len(tools)+1)
	copy(result, tools)
	result = append(result, &agentic.SendMessageTool{
		Bus:      am.agentBus,
		FromName: "main",
	})
	return result
}

func (am *AgentManager) wireMainAgent(agent *agentic.Agent) {
	if am.agentBus == nil {
		return
	}
	am.agentBus.Unregister("main")
	inbox, err := am.agentBus.Register("main")
	if err != nil {
		am.emitFlash("Failed to register main agent on bus: " + err.Error())
		return
	}
	am.mainConnector = agentic.NewCommConnector(agent, inbox)
}

// SendUserInput sends a user message to the active agent.
func (am *AgentManager) SendUserInput(input string) error {
	return am.SendUserInputWithImages(input, nil)
}

// SendUserInputWithImages sends a user message with optional image attachments.
func (am *AgentManager) SendUserInputWithImages(input string, images []string) error {
	am.mu.Lock()
	am.lastUserInput = input
	agent := am.activeAgent
	alreadyRunning := am.running
	am.mu.Unlock()

	if orch := am.foregroundOrchestrator(); orch != nil {
		orch.ResetCompanionCount()
	}

	if agent == nil {
		return fmt.Errorf("no active session")
	}

	am.turnRecorder.ResetTurn(time.Now())

	// Queue as steering whenever the agent cannot start a user turn right now:
	// a manager-owned turn is in flight (alreadyRunning) OR an externally
	// driven turn owns the agent (e.g. a goal continuation turn — the goal
	// driver calls agent.Run directly, so alreadyRunning stays false while
	// the agent is processing). Without the IsProcessing half, such input
	// spawned a phantom runAgentTurn that returned instantly on the agent's
	// internal queue — never woven mid-turn, and stranded if the in-flight
	// turn errored or was cancelled.
	if alreadyRunning || (agent != nil && agent.IsProcessing()) {
		am.steering.Append(input)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	am.mu.Lock()
	am.cancelGen++
	gen := am.cancelGen
	am.running = true
	am.cancel = cancel
	am.mu.Unlock()

	go am.runAgentTurn(ctx, cancel, gen, agent, input, images)
	return nil
}

// steeringSourceAdapter adapts *SteeringQueue (Flush) to the agentic
// SteeringSource interface (Drain) so the agent can poll for mid-turn steering
// between stream rounds.
type steeringSourceAdapter struct{ q *SteeringQueue }

func (a steeringSourceAdapter) Drain() []string { return a.q.Flush() }

func (am *AgentManager) runAgentTurn(ctx context.Context, cancel context.CancelFunc, gen int, runner agentRunner, input string, images []string) {
	// turnEndedCleanly gates the post-turn hook: a panicking turn must not
	// re-drive goals (the agent may panic again). The hook itself runs in the
	// cleanup defer — AFTER am.running is cleared and steering dispatched —
	// because the goal driver started by the hook must never observe the
	// agent as still busy with this turn (Issue 7: a drive started
	// while the agent is mid-turn queue-storms continuation prompts).
	turnEndedCleanly := false
	defer am.recoverTurnPanic()
	defer func() {
		cancel()
		am.mu.Lock()
		if am.cancelGen == gen {
			am.cancel = nil
		}
		am.running = false
		// Capture pending steering while holding the lock so finalizeTurn
		// cannot overwrite it after we release.
		pending := am.pendingSteering
		am.pendingSteering = ""
		am.mu.Unlock()

		// Dispatch steering only after am.running is false, so the
		// alreadyRunning check in SendUserInput does not re-queue.
		if pending != "" {
			am.emitSteeringInjected(pending)
			_ = am.SendUserInput(pending)
		}

		if turnEndedCleanly && am.postTurnHook != nil {
			am.postTurnHook()
		}
	}()

	am.applyPendingMajorMode()
	am.applyPendingThinkingLevel()
	am.mu.Lock()
	am.loopStopReason = ""
	am.mu.Unlock()
	// A genuine new user turn resets the agent's runaway-loop latch and
	// repeat counters: the guardrail stops a runaway exchange, never the
	// session (runaway-loop bricking). Goal continuation turns
	// bypass runAgentTurn and keep their guardrail state so cross-turn
	// driver loops still latch. Optional interface: test runners need not
	// implement it.
	if lr, ok := runner.(interface{ ResetLoopStop() }); ok {
		lr.ResetLoopStop()
	}
	// Defense in depth: a turn that ends without EventEnd (loop-detector
	// interrupt, user Escape, stream error) skips finalizeTurn and would leak
	// its thinking-repeat counters into this turn, instantly re-triggering the
	// detector on the first delta. Start every turn with a clean slate.
	if am.loopDetector != nil {
		am.loopDetector.ResetThinking()
	}
	am.executeRunner(ctx, runner, input, images)

	// After the runner finishes, flush any steering still pending and queue it
	// as the next user turn. Mid-turn steering is normally already woven into
	// the turn by the agent's between-round drain (SetSteeringSource); only
	// leftovers remain — steering typed during the final no-tool round (which
	// has no subsequent round to drain into), or during a turn that errored or
	// was interrupted before draining.
	am.mu.Lock()
	pending := am.steering.Flush()
	if len(pending) > 0 {
		am.pendingSteering = strings.Join(pending, "\n\n")
	}
	am.mu.Unlock()

	turnEndedCleanly = true
}

func (am *AgentManager) emitSteeringInjected(text string) {
	am.emitChat(event.ChatEvent{SteeringInjected: &event.SteeringInput{Text: text}})
}

func (am *AgentManager) emitChat(ev event.ChatEvent) {
	if am.eventsOut == nil {
		return
	}
	select {
	case am.eventsOut.Chat <- ev:
	default:
	}
}

// recoverTurnPanic converts an agent-turn panic into an EventEnd so the UI
// marks the turn complete and the user sees that the agent stopped.
func (am *AgentManager) recoverTurnPanic() {
	if r := recover(); r != nil {
		if am.logger != nil {
			am.logger.Log(agentic.Error, "agent turn panic: %v", r)
		}
		ev := agentic.OutputEvent{
			Type: agentic.EventEnd,
			Text: fmt.Sprintf("agent stopped unexpectedly: %v", r),
		}
		am.emitAgentEvent(ev)
		am.emitInternalEvent(ev)
	}
}

// executeRunner runs the agent and emits an EventEnd on error. It is split
// out of runAgentTurn to keep the turn lifecycle within the complexity budget.
func (am *AgentManager) executeRunner(ctx context.Context, runner agentRunner, input string, images []string) {
	var err error
	if len(images) > 0 {
		err = runner.RunWithImages(ctx, input, images)
	} else {
		err = runner.Run(ctx, input)
	}
	if err != nil {
		ev := agentic.OutputEvent{Type: agentic.EventEnd}
		if errors.Is(err, context.Canceled) {
			am.mu.Lock()
			reason := am.loopStopReason
			am.mu.Unlock()
			if reason != "" {
				// Loop detector cancelled the turn; surface the real reason
				// so the UI does not show the generic "user stopped" message.
				ev.Text = reason
			} else {
				// Distinguish user-initiated cancellation from transport aborts.
				// When the agent's retry logic has already filtered out transport
				// drops (ctx.Err() == nil), a context.Canceled here means the user
				// pressed Escape/Ctrl+C. The metadata "cancelled: user" tells the
				// UI to show "Generation stopped by user." instead of a generic
				// cancellation message.
				ev.Metadata = map[string]string{"cancelled": "true", "cancel_source": "user"}
			}
		} else {
			ev.Text = err.Error()
		}
		// EventEnd must always reach the UI so the turn is marked complete; do
		// not drop it under load (CORE-BUG-3). Block (backpressure) instead.
		am.emitAgentEvent(ev)
		am.emitInternalEvent(ev)
	}
}

// StopSession stops the active agent session.
func (am *AgentManager) StopSession() error {
	am.mu.Lock()
	defer am.mu.Unlock()

	if am.activeAgent == nil {
		return nil
	}

	if am.cancel != nil {
		am.cancel()
		am.cancel = nil
	}

	if am.mainConnector != nil {
		am.mainConnector.Stop()
		am.mainConnector = nil
	}
	if am.agentBus != nil {
		am.agentBus.Unregister("main")
	}

	am.activeAgent = nil
	am.dispatchLifecycle("shutdown", map[string]any{})
	am.fireSessionEnd()
	return nil
}

// Close releases long-lived resources (the event forwarder goroutine). It is
// idempotent and safe to call at shutdown. StopSession should be called first
// to stop any active turn; Close does not cancel an in-flight turn.
func (am *AgentManager) Close() {
	am.mu.Lock()
	fwd := am.eventFwd
	am.eventFwd = nil
	am.mu.Unlock()
	if fwd != nil {
		fwd.close()
	}
}

func (am *AgentManager) fireSessionStart(sessionID string) {
	if am.hookEngine == nil {
		return
	}
	_ = am.hookEngine.FireSessionStart(context.Background(), hooks.SessionPayload{
		Event:      string(hooks.EventSessionStart),
		SessionID:  sessionID,
		ProjectDir: am.projectDir,
	})
}

func (am *AgentManager) fireSessionEnd() {
	if am.hookEngine == nil {
		return
	}
	_ = am.hookEngine.FireSessionEnd(context.Background(), hooks.SessionPayload{
		Event:      string(hooks.EventSessionEnd),
		SessionID:  "",
		ProjectDir: am.projectDir,
	})
}

// Interrupt cancels the current agent turn.
// Logs the caller's identity so that unexpected cancellations (e.g. from
// transport-level aborts misrouted through the cancel path) can be traced.
func (am *AgentManager) Interrupt() error {
	_, file, line, _ := runtime.Caller(1)
	if am.logger != nil {
		am.logger.Log(agentic.Info, "Interrupt() called from %s:%d", file, line)
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.cancel != nil {
		am.cancel()
		am.cancel = nil
	}
	return nil
}

// CurrentAgent returns the active agent.
func (am *AgentManager) CurrentAgent() *agentic.Agent {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.activeAgent
}

// IsBusy reports whether the agent is unavailable for a new user turn: either
// a manager-owned turn is in flight (IsRunning) or the agent is executing an
// externally driven turn — e.g. a goal continuation turn from GoalDriver,
// which calls agent.Run directly and never flips am.running. Steering routing
// must use IsBusy: gating on IsRunning alone let user input typed during goal
// turns bypass the steering queue entirely (dispatched as a phantom normal
// message into the agent's internal queue, never woven mid-turn, and stranded
// there if the in-flight turn errored or was cancelled).
func (am *AgentManager) IsBusy() bool {
	am.mu.Lock()
	running := am.running
	agent := am.activeAgent
	am.mu.Unlock()
	if running {
		return true
	}
	return agent != nil && agent.IsProcessing()
}

// DispatchPendingSteering dispatches steering left over from a turn that did
// not pass through runAgentTurn — externally driven goal continuation turns
// (agentManagerRunner). Two buffers must drain: pendingSteering, where the
// finalizeTurn observer (EventEnd fires for goal turns too) stashes leftover
// steering, and the live steering queue, which catches anything appended
// after EventEnd. Without this dispatch the stashed text sits in
// pendingSteering until some unrelated future user turn (or forever) and the
// pending bubble never clears. Emits SteeringInjected so the UI clears the
// bubble and renders the consumed text. No-op when both buffers are empty.
func (am *AgentManager) DispatchPendingSteering() {
	am.mu.Lock()
	stashed := am.pendingSteering
	am.pendingSteering = ""
	am.mu.Unlock()

	pending := am.steering.Flush()
	if stashed != "" {
		pending = append([]string{stashed}, pending...)
	}
	if len(pending) == 0 {
		return
	}
	text := strings.Join(pending, "\n\n")
	am.emitSteeringInjected(text)
	_ = am.SendUserInput(text)
}

// IsRunning reports whether a user turn is currently in progress.
func (am *AgentManager) IsRunning() bool {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.running
}

// SteeringQueue returns the session's steering queue. The TUI uses it to
// append user input while the agent is running.
func (am *AgentManager) SteeringQueue() *SteeringQueue {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.steering
}

// SetSteeringQueue replaces the steering queue. Used by tests and by wiring
// code that wants a shared queue instance.
func (am *AgentManager) SetSteeringQueue(sq *SteeringQueue) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.steering = sq
}

// LastUserInput returns the last user message.
func (am *AgentManager) LastUserInput() string {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.lastUserInput
}

// SetLastUserInputForTest is exported only for tests in dependent packages
// that need to simulate a conversation having started.
func (am *AgentManager) SetLastUserInputForTest(input string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.lastUserInput = input
}

// SystemPrompt returns the current system prompt.
func (am *AgentManager) SystemPrompt() string {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.systemPrompt
}

// augmentSystemPrompt combines the base prompt with optional companion review
// and agent-driven additions. The additions are kept in a deterministic order
// so the resulting prompt always reflects the latest status.
func (am *AgentManager) augmentSystemPrompt(base string) string {
	parts := []string{base}
	if am.companionReviewSet {
		if am.companionReviewEnabled {
			if p, err := prompts.LoadCompanionReviewEnabledPrompt(); err == nil && p != "" {
				parts = append(parts, p)
			}
		} else {
			if p, err := prompts.LoadCompanionReviewDisabledPrompt(); err == nil && p != "" {
				parts = append(parts, p)
			}
		}
	}
	if am.modeMgr.AgentDrivenEnabled() {
		if p := am.modeMgr.AgentDrivenPrompt(); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "\n\n")
}

// InjectCompanionReview updates the system prompt to reflect whether companion
// review is enabled. When a session is active, it replaces any previous
// companion-review system message in the conversation history with a single
// message containing the latest status.
func (am *AgentManager) InjectCompanionReview(enabled bool) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	am.companionReviewEnabled = enabled
	am.companionReviewSet = true
	am.systemPrompt = am.augmentSystemPrompt(am.baseSystemPrompt)

	if am.activeAgent == nil {
		return nil
	}

	var prompt string
	var err error
	if enabled {
		prompt, err = prompts.LoadCompanionReviewEnabledPrompt()
	} else {
		prompt, err = prompts.LoadCompanionReviewDisabledPrompt()
	}
	if err != nil {
		return fmt.Errorf("load companion review prompt: %w", err)
	}

	history := am.activeAgent.GetHistory()
	history = filterCompanionReviewMessages(history)
	history = append(history, agentic.Message{
		Type:    agentic.Content,
		Role:    agentic.System,
		Content: prompt,
	})
	am.activeAgent.SetHistory(history)
	return nil
}

// filterCompanionReviewMessages removes system messages that were injected to
// communicate companion review status. Only the latest status is kept.
func filterCompanionReviewMessages(history []agentic.Message) []agentic.Message {
	out := make([]agentic.Message, 0, len(history))
	for _, m := range history {
		if m.Role == agentic.System && strings.HasPrefix(m.Content, "Companion review is") {
			continue
		}
		out = append(out, m)
	}
	return out
}

// Events returns the event channel for TUI consumption.
func (am *AgentManager) Events() <-chan agentic.OutputEvent {
	return am.events
}

// SetLogger configures the agentic SDK logger.
func (am *AgentManager) SetLogger(logger *agentic.Logger) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.logger = logger
	if am.sessionStore != nil {
		am.sessionStore.SetLogger(logger)
	}
}

// TriggerCompression manually triggers context compression.
func (am *AgentManager) TriggerCompression(ctx context.Context) error {
	return am.TriggerCompressionWith(ctx, "", true)
}

// TriggerCompressionWith manually triggers context compression using the
// given strategy. An empty strategy falls back to the configured one.
// When force is true, internal per-strategy thresholds are bypassed.
func (am *AgentManager) TriggerCompressionWith(ctx context.Context, strategy string, force bool) error {
	am.mu.Lock()
	agent := am.activeAgent
	am.mu.Unlock()
	if agent == nil {
		return fmt.Errorf("no active agent session")
	}
	return agent.MaybeCompressWith(ctx, agentic.CompressionStrategy(strategy), force)
}

// SetActiveAgentForTest binds a prebuilt agent to the manager. Test-only:
// production code must go through StartSession so the agent is wired with
// observers, tools, mode state, and session persistence.
func (am *AgentManager) SetActiveAgentForTest(agent *agentic.Agent) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.activeAgent = agent
}

// SetRunningForTest sets the turn-in-progress flag. Test-only: production
// code transitions it via SendUserInput/runAgentTurn.
func (am *AgentManager) SetRunningForTest(running bool) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.running = running
}

// SetTools updates the tools available to the active agent. Changes take
// effect on the next turn without restarting the session.
func (am *AgentManager) SetTools(tools []agentic.Tool) error {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.activeAgent == nil {
		return fmt.Errorf("no active agent session")
	}
	am.activeAgent.SetTools(am.toolsWithBus(tools))
	return nil
}

// SetModeRegistry sets the ModeRegistry used for resolving mode definitions.
// Must be set before StartSession or SetMode for mode prompt injection to work.
func (am *AgentManager) SetModeRegistry(reg *ModeRegistry) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.modeRegistry = reg
}

// injectModePrompt injects the body of the given mode as a system message
// so the agent's role instructions update to match the new mode.
// Caller must NOT hold am.mu.
func (am *AgentManager) injectModePrompt(major internal.MajorMode) {
	if am.modeRegistry == nil || am.activeAgent == nil {
		return
	}
	body := am.modeRegistry.SystemPrompt(major)
	if body == "" {
		return
	}
	msg := fmt.Sprintf("You have switched to %s mode. Your new instructions:\n\n%s", major, body)
	am.activeAgent.InjectSystemMessage(msg)
}

// InjectSystemMessage appends a system message to the active agent's history.
// It returns an error if no agent session is active.
//
// CAUTION: the agent.InjectSystemMessage call triggers event emission that
// calls back into the AgentManager (OnEvent → logEvent), which acquires
// am.mu. To avoid a self-deadlock with a non-reentrant mutex, the agent
// pointer is snapshot under the lock and the call is made outside it.
func (am *AgentManager) InjectSystemMessage(content string) error {
	am.mu.Lock()
	agent := am.activeAgent
	am.mu.Unlock()
	if agent == nil {
		return fmt.Errorf("no active agent session")
	}
	agent.InjectSystemMessage(content)
	return nil
}

// SetModel replaces the active agent's model for subsequent turns and syncs
// the context compression configuration so the new model's context window and
// any per-model compression overrides are used for ceiling/compaction
// decisions.
//
// It also restores the thinking level saved for the new model (per-model
// thinking_level, falling back to the global thinking_levels defaults), so
// changing one model's level never leaks onto another. Callers must update
// cfg.ActiveModel before invoking SetModel — the level is resolved from the
// config's active model.
func (am *AgentManager) SetModel(mdl agenticprovider.Model) {
	am.mu.Lock()
	if am.activeAgent == nil {
		am.mu.Unlock()
		return
	}
	am.activeAgent.SetModel(mdl)
	compressionCfg := am.buildCompressionConfig(am.cfg, mdl.ID, mdl.ContextWindow)
	if compressionCfg.MaxTokens > 0 || am.hasCompressionOverride(mdl.ID) {
		am.activeAgent.SetContextCompression(compressionCfg)
	}
	am.mu.Unlock()
	am.syncThinkingLevelForActiveModel()
}

// syncThinkingLevelForActiveModel re-resolves the thinking level for the
// config's active model and applies it to the session (mode state, queued
// agent change, persisted snapshot, footer event). Unlike SetThinkingLevel
// it does not write the model config entry — the value comes FROM the
// config, so saving it back would be a no-op write.
func (am *AgentManager) syncThinkingLevelForActiveModel() {
	if am.cfg == nil {
		return
	}
	level := string(am.cfg.GetThinkingLevel("main_agent"))
	if err := am.applySessionThinkingLevel(level); err != nil && am.logger != nil {
		am.logger.Log(agentic.Warn, "failed to persist thinking level after model switch: %v", err)
	}
}

// hasCompressionOverride reports whether a per-model compression override
// exists for the given model ID (in which case SetModel must re-apply the
// compression config even when MaxTokens is 0/auto).
func (am *AgentManager) hasCompressionOverride(modelID string) bool {
	if modelID == "" {
		return false
	}
	_, ok := am.cfg.ContextCompression.PerModel[modelID]
	return ok
}

// RefreshContextCompression re-resolves the compression config for the
// active model (including per-model overrides) and applies it to the active
// agent, so /config changes to context_compression take effect immediately
// instead of on the next session or model switch.
func (am *AgentManager) RefreshContextCompression() {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.activeAgent == nil {
		return
	}
	mdl := am.activeAgent.Model()
	compressionCfg := am.buildCompressionConfig(am.cfg, mdl.ID, mdl.ContextWindow)
	if am.cfg.ContextCompression.EnabledValue() || compressionCfg.MaxTokens > 0 {
		am.activeAgent.SetContextCompression(compressionCfg)
	}
}

// SetStreamOptions replaces the active agent's stream options for subsequent turns.
// This updates the API key, headers, timeout, and other provider settings so the
// new provider's credentials are used on the next turn.
//
// Rule 7 (append-only conversations; cache IDs are context-scoped): a
// provider/model switch mid-session does NOT begin a new context — the
// conversation continues as an append of itself, so it must keep its
// SessionID (the provider cache key). An empty incoming SessionID therefore
// inherits the live one; an explicitly-set SessionID (e.g. static provider
// config) is a deliberate override and wins. Rotation stays with
// ResetConversationID, which writes the agent's options directly.
func (am *AgentManager) SetStreamOptions(opts agenticprovider.StreamOptions) {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.activeAgent != nil {
		if opts.SessionID == "" {
			opts.SessionID = am.activeAgent.StreamOptions().SessionID
		}
		am.activeAgent.SetStreamOptions(opts)
	}
}

// ResetConversationID mints a fresh conversation/session id and applies it to
// the active agent's StreamOptions for subsequent turns. It returns the new id
// ("" when no session store or no active agent).
//
// Used when a logically-new conversation begins on the SAME agent — notably a
// fresh-context goal (RunFresh begin=true), which clears the agent's history but
