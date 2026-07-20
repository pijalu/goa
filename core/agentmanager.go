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
	sessionStore   *SessionStore // event recording store
	stateStore     *StateStore   // persisted mode state store
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
	postTurnHook         func()
	lifecycleRegistry    LifecycleRegistry
	projectDir           string
	confirmTool          func(ctx context.Context, toolName, input string) (bool, error)

	// goalTokenRecorder is called with the latest total token count for
	// the active agent turn. Used by the goal system to track token budget.
	goalTokenRecorder func(totalTokens int)

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

	if alreadyRunning {
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

func (am *AgentManager) runAgentTurn(ctx context.Context, cancel context.CancelFunc, gen int, runner agentRunner, input string, images []string) {
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
	}()

	am.applyPendingMajorMode()
	am.applyPendingThinkingLevel()
	am.mu.Lock()
	am.loopStopReason = ""
	am.mu.Unlock()
	am.executeRunner(ctx, runner, input, images)

	// After the runner finishes, flush any steering input submitted while the
	// turn was running and queue it as the next user turn.
	am.mu.Lock()
	pending := am.steering.Flush()
	if len(pending) > 0 {
		am.pendingSteering = strings.Join(pending, "\n\n")
	}
	am.mu.Unlock()

	if am.postTurnHook != nil {
		am.postTurnHook()
	}
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
				// User-initiated cancellation (Escape/Ctrl+C) is not a connection
				// error; mark it so the UI can stop gracefully and keep the
				// conversation resumable from the user's last message.
				ev.Metadata = map[string]string{"cancelled": "true"}
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
func (am *AgentManager) SetModel(mdl agenticprovider.Model) {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.activeAgent == nil {
		return
	}
	am.activeAgent.SetModel(mdl)
	compressionCfg := am.buildCompressionConfig(am.cfg, mdl.ID, mdl.ContextWindow)
	if compressionCfg.MaxTokens > 0 || am.hasCompressionOverride(mdl.ID) {
		am.activeAgent.SetContextCompression(compressionCfg)
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
	if am.cfg.ContextCompression.Enabled || compressionCfg.MaxTokens > 0 {
		am.activeAgent.SetContextCompression(compressionCfg)
	}
}

// SetStreamOptions replaces the active agent's stream options for subsequent turns.
// This updates the API key, headers, timeout, and other provider settings so the
// new provider's credentials are used on the next turn.
func (am *AgentManager) SetStreamOptions(opts agenticprovider.StreamOptions) {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.activeAgent != nil {
		am.activeAgent.SetStreamOptions(opts)
	}
}

// ActiveModel returns the active agent's model, or an empty model if none.
func (am *AgentManager) ActiveModel() agenticprovider.Model {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.activeAgent == nil {
		return agenticprovider.Model{}
	}
	return am.activeAgent.Model()
}

// TurnHistory returns all completed turns.
func (am *AgentManager) TurnHistory() []TurnRecord {
	return am.turnRecorder.TurnHistory()
}

// LastTurn returns the most recent completed turn.
func (am *AgentManager) LastTurn() *TurnRecord {
	return am.turnRecorder.LastTurn()
}

// EmitEvent sends a chat flash message.
func (am *AgentManager) EmitEvent(text string) {
	am.emitFlash(text)
}

// SetForegroundOrchestrator sets the orchestrator. Guarded by am.mu because
// the field is read from the observer goroutine during a turn; an unlocked
// write here raced with handleToolCallEvent/SendUserInput reads (CORE-BUG-2).
func (am *AgentManager) SetForegroundOrchestrator(orch *multiagent.ForegroundOrchestrator) {
	am.mu.Lock()
	am.foregroundOrch = orch
	am.mu.Unlock()
	am.companion.SetForegroundOrchestrator(orch)
}

// SetCompanionTimeout configures how long framework-driven companion reviews
// are allowed to run before cancellation.
func (am *AgentManager) SetCompanionTimeout(d time.Duration) {
	am.companion.SetMessageTimeout(d)
}

// SetStateStore sets the state store for persisting mode changes. Guarded by
// am.mu: the field is read concurrently from SetInputHistory/GetInputHistory/
// persistState while a turn is driving (CORE-BUG-2).
func (am *AgentManager) SetStateStore(ss *StateStore) {
	am.mu.Lock()
	am.stateStore = ss
	am.mu.Unlock()
	am.modeMgr.SetStateStore(ss)
}

// foregroundOrchestrator returns the current foreground orchestrator snapshot
// under am.mu. Callers must not assume the pointer stays valid beyond the call;
// they should only invoke stateless methods on it.
func (am *AgentManager) foregroundOrchestrator() *multiagent.ForegroundOrchestrator {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.foregroundOrch
}

// stateStoreRef returns the current state store snapshot under am.mu.
func (am *AgentManager) stateStoreRef() *StateStore {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.stateStore
}

// SetInputHistory persists the input history to the state store.
// Deprecated: Input history is now per-session. This is a no-op.
func (am *AgentManager) SetInputHistory(history []string) error {
	return nil
}

// GetInputHistory loads the input history from the state store.
// Deprecated: Use per-session input history files instead.
func (am *AgentManager) GetInputHistory() []string {
	return nil
}

// SessionID returns the current session ID, or empty if no session is active.
func (am *AgentManager) SessionID() string {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.sessionStore == nil {
		return ""
	}
	return am.sessionStore.SessionID()
}

// SetPendingInputHistory stores input history for the editor after session
// restore. The app layer retrieves it via GetAndClearPendingInputHistory.
func (am *AgentManager) SetPendingInputHistory(h []string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.pendingInputHistory = h
}

// GetAndClearPendingInputHistory returns any pending input history and clears
// it. Used by the app layer after command execution to populate the editor.
func (am *AgentManager) GetAndClearPendingInputHistory() []string {
	am.mu.Lock()
	defer am.mu.Unlock()
	h := am.pendingInputHistory
	am.pendingInputHistory = nil
	return h
}

// SetAgentDrivenEnabled sets whether agent-driven tools are enabled.
func (am *AgentManager) SetAgentDrivenEnabled(enabled bool) error {
	if err := am.modeMgr.SetAgentDrivenEnabled(enabled); err != nil {
		am.emitFlash("Failed to load agent-driven prompt: " + err.Error())
	}
	if err := am.persistState(); err != nil {
		return fmt.Errorf("failed to save agent-driven state: %w", err)
	}
	return nil
}

// AgentDrivenEnabled returns whether agent-driven tools are active.
func (am *AgentManager) AgentDrivenEnabled() bool {
	return am.modeMgr.AgentDrivenEnabled()
}

// AgentDrivenPrompt returns the current agent-driven system prompt addition.
func (am *AgentManager) AgentDrivenPrompt() string {
	return am.modeMgr.AgentDrivenPrompt()
}

// SetAgentDrivenChangeCallback registers a callback invoked when the
// agent-driven enabled state changes.
func (am *AgentManager) SetAgentDrivenChangeCallback(cb func(bool)) {
	am.agentDriven.SetChangeCallback(cb)
}

// CurrentMode returns the current ModeState.
func (am *AgentManager) CurrentMode() internal.ModeState {
	return am.modeMgr.CurrentMode()
}

// SetMode replaces the current mode. If a session is active, the new mode's
// system prompt is queued for injection at the start of the next turn; a chat
// bubble notifies the user immediately.
func (am *AgentManager) SetMode(ms internal.ModeState) internal.ModeState {
	old := am.CurrentMode()
	if info := am.modeMgr.SetMode(ms); info != nil {
		am.emitModeChange(info.OldMode, info.NewMode, info.Source)
		am.emitModeChangeFlash(info.OldMode, info.NewMode)
		am.dispatchLifecycle("mode_enter", map[string]any{
			"old_mode": string(old.Major),
			"new_mode": string(info.NewMode.Major),
			"autonomy": string(info.NewMode.Autonomy),
			"source":   info.Source,
		})
		am.queueMajorModePrompt(ms.Major)
	}
	if err := am.persistState(); err != nil {
		am.emitFlash("Failed to save mode state: " + err.Error())
	}
	return ms
}

// PushMode saves current and activates a new mode.
func (am *AgentManager) PushMode(ms internal.ModeState, source string) internal.ModeState {
	old := am.CurrentMode()
	info := am.modeMgr.PushMode(ms, source)
	if info != nil {
		am.emitModeChange(info.OldMode, info.NewMode, info.Source)
		am.emitModeChangeFlash(info.OldMode, info.NewMode)
		am.dispatchLifecycle("mode_enter", map[string]any{
			"old_mode": string(old.Major),
			"new_mode": string(info.NewMode.Major),
			"autonomy": string(info.NewMode.Autonomy),
			"source":   info.Source,
		})
		am.queueMajorModePrompt(info.NewMode.Major)
	}
	if err := am.persistState(); err != nil {
		am.emitFlash("Failed to save mode state: " + err.Error())
	}
	if info == nil {
		return ms
	}
	return info.OldMode
}

// PopMode restores the previous mode from the stack and emits event.
func (am *AgentManager) PopMode() internal.ModeState {
	old := am.CurrentMode()
	info := am.modeMgr.PopMode()
	if info != nil {
		am.emitModeChange(info.OldMode, info.NewMode, info.Source)
		am.emitModeChangeFlash(info.OldMode, info.NewMode)
		am.dispatchLifecycle("mode_enter", map[string]any{
			"old_mode": string(old.Major),
			"new_mode": string(info.NewMode.Major),
			"autonomy": string(info.NewMode.Autonomy),
			"source":   info.Source,
		})
		am.queueMajorModePrompt(info.NewMode.Major)
	}
	if err := am.persistState(); err != nil {
		am.emitFlash("Failed to save mode state: " + err.Error())
	}
	if info == nil {
		return internal.ModeState{}
	}
	return info.NewMode
}

// PreviousMode returns the mode before the last push.
func (am *AgentManager) PreviousMode() *internal.ModeState {
	return am.modeMgr.PreviousMode()
}

// Source returns the source of the current pushed mode.
func (am *AgentManager) Source() string {
	return am.modeMgr.Source()
}

// SetCompanionAgent stores the companion agent.
func (am *AgentManager) SetCompanionAgent(a *agentic.Agent) {
	am.companion.SetCompanionAgent(a, am.agentBus)
}

// SetMinorMode enables or disables a minor mode and persists the change.
func (am *AgentManager) SetMinorMode(mode string, enabled bool) error {
	orch := am.foregroundOrchestrator()
	if orch == nil {
		return fmt.Errorf("no orchestrator available")
	}
	switch mode {
	case "companion":
		if enabled {
			orch.SetMode(multiagent.WorkflowAgentDriven)
		} else {
			orch.SetMode(multiagent.WorkflowInactive)
		}
		if err := am.modeMgr.SetAgentDrivenEnabled(enabled); err != nil {
			return fmt.Errorf("failed to sync agent-driven state: %w", err)
		}
	default:
		return fmt.Errorf("unknown minor mode: %q", mode)
	}
	activeMode := ""
	if enabled {
		activeMode = mode
	}
	am.modeMgr.SetCurrentMinorMode(activeMode)

	if err := am.persistState(); err != nil {
		return fmt.Errorf("failed to save minor mode: %w", err)
	}
	am.emitMinorMode(activeMode)
	return nil
}

// SetThinkingLevel sets the reasoning effort level, persists it, and queues
// the change for the active agent session. The new level takes effect on the
// next turn so the current turn is not interrupted.
func (am *AgentManager) SetThinkingLevel(level string) error {
	am.modeMgr.SetThinkingLevel(level)
	am.queueThinkingLevel(level)
	if err := am.persistState(); err != nil {
		return fmt.Errorf("failed to save thinking level: %w", err)
	}
	am.emitThinkingLevel(level)
	return nil
}

func (am *AgentManager) queueThinkingLevel(level string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.activeAgent == nil {
		return
	}
	am.pendingThinkingLevel = &level
}

// applyPendingThinkingLevel applies any queued thinking-level change to the
// active agent before a new turn begins.
func (am *AgentManager) applyPendingThinkingLevel() {
	am.mu.Lock()
	pending := am.pendingThinkingLevel
	am.pendingThinkingLevel = nil
	am.mu.Unlock()
	if pending != nil && am.activeAgent != nil {
		am.activeAgent.SetReasoningEffort(agentic.ReasoningEffort(*pending))
	}
}

// GetThinkingLevel returns the current thinking level.
func (am *AgentManager) GetThinkingLevel() string {
	return am.modeMgr.GetThinkingLevel()
}

func (am *AgentManager) persistState() error {
	ss := am.stateStoreRef()
	if ss == nil {
		return nil
	}
	snap := SessionStateSnapshot{
		ModeState:          am.modeMgr.CurrentMode(),
		MinorMode:          am.modeMgr.CurrentMinorMode(),
		AgentDrivenEnabled: am.modeMgr.AgentDrivenEnabled(),
		ThinkingLevel:      am.modeMgr.GetThinkingLevel(),
	}
	if hist := MarshalCompanionHistory(am.companion.Agent()); len(hist) > 0 {
		snap.CompanionHistory = hist
	}
	return ss.Save(snap)
}

func (am *AgentManager) emitInternalEvent(ev agentic.OutputEvent) {
	if am.forwardInternalEvents {
		am.events <- ev
	}
}

func (am *AgentManager) emitAgentEvent(ev agentic.OutputEvent) {
	if am.eventsOut == nil || am.eventFwd == nil {
		return
	}
	// Forward through an unbounded queue so the LLM stream goroutine never
	// blocks on the bounded app bus. A slow TUI falls behind in the forwarder,
	// not in token generation.
	am.eventFwd.push(event.AgentEvent{Event: ev})
}

func (am *AgentManager) emitFlash(text string) {
	if am.eventsOut == nil {
		return
	}
	select {
	case am.eventsOut.Chat <- event.ChatEvent{Flash: &event.Flash{Text: text}}:
	default:
	}
}

func (am *AgentManager) emitModeChange(old, new internal.ModeState, source string) {
	if am.eventsOut == nil {
		return
	}
	select {
	case am.eventsOut.Footer <- event.FooterEvent{ModeChange: &event.ModeChange{OldMode: old, NewMode: new, Source: source}}:
	default:
	}
}

func (am *AgentManager) emitModeChangeFlash(old, new internal.ModeState) {
	if am.eventsOut == nil {
		return
	}
	am.mu.Lock()
	active := am.activeAgent != nil
	am.mu.Unlock()
	if !active {
		return
	}
	var text string
	switch {
	case old.Major != new.Major:
		text = fmt.Sprintf("Mode: %s", new.Major)
	case old.Autonomy != new.Autonomy:
		text = fmt.Sprintf("Autonomy: %s", new.Autonomy)
	default:
		return
	}
	select {
	case am.eventsOut.Chat <- event.ChatEvent{Flash: &event.Flash{Text: text}}:
	default:
	}
}

func (am *AgentManager) queueMajorModePrompt(major internal.MajorMode) {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.activeAgent == nil {
		return
	}
	am.pendingMajor = &major
}

// applyPendingMajorMode injects any queued mode prompt before a new turn.
func (am *AgentManager) applyPendingMajorMode() {
	am.mu.Lock()
	pending := am.pendingMajor
	am.pendingMajor = nil
	am.mu.Unlock()
	if pending != nil {
		am.injectModePrompt(*pending)
	}
}

func (am *AgentManager) emitMinorMode(mode string) {
	if am.eventsOut == nil {
		return
	}
	select {
	case am.eventsOut.Footer <- event.FooterEvent{MinorMode: &event.MinorMode{Mode: mode}}:
	default:
	}
}

func (am *AgentManager) emitThinkingLevel(level string) {
	if am.eventsOut == nil {
		return
	}
	select {
	case am.eventsOut.Footer <- event.FooterEvent{ThinkingLevel: &event.ThinkingLevel{Level: level}}:
	default:
	}
}

// handleThinkingLoopWarning acts on a thinking/reasoning loop detected by
// RecordThinkingDelta. Unlike tool loops, repeated reasoning is only flashed
// at the warning level (models sometimes restate a plan) but still interrupts
// at the interrupt level — an assistant stuck emitting the identical paragraph
// will otherwise burn the whole context window.
func (am *AgentManager) handleThinkingLoopWarning(lvl LoopWarningLevel) {
	if lvl <= LoopOK {
		return
	}
	switch lvl {
	case LoopWarning:
		am.logEventF("loop detector: warning (thinking repeat)")
		am.emitFlash("[goa-system: warning] Reasoning is repeating — the model may be stuck in a thinking loop.")
	case LoopCritical, LoopInterrupt:
		am.logEventF("loop detector: interrupt — thinking loop detected, cancelling turn")
		am.emitFlash("[goa-system: interrupt] Thinking loop detected — cancelling turn.")
		am.Interrupt()
	}
}

// handleLoopWarning processes a loop detector warning level and takes action.
func (am *AgentManager) handleLoopWarning(lvl LoopWarningLevel) {
	if lvl <= LoopOK {
		return
	}
	switch lvl {
	case LoopWarning:
		am.logEventF("loop detector: warning (tool repeat)")
		am.emitFlash("[goa-system: warning] Tool call repeated — consider completing the task.")
	case LoopCritical:
		// STUB-2: previously this branch only logged/ flashed a "will be paused"
		// message without actually pausing. A critical loop must act, so interrupt
		// the turn (same effect as LoopInterrupt) rather than promising a pause
		// that never happens.
		am.logEventF("loop detector: critical — cancelling turn")
		am.emitFlash("[goa-system: critical] Agent looping — cancelling turn.")
		am.setLoopStopReason("[goa-system] Agent stopped: the same tool call was repeated too many times without progress. Change approach or provide the final answer.")
		am.Interrupt()
	case LoopInterrupt:
		am.logEventF("loop detector: interrupt — cancelling turn")
		am.emitFlash("[goa-system: interrupt] Tool call loop detected — cancelling turn.")
		am.setLoopStopReason("[goa-system] Agent stopped: a tool-call loop was detected (the same call repeated too many times). Change approach or provide the final answer.")
		am.Interrupt()
	}
}

// setLoopStopReason records why the loop detector cancelled the turn. Called
// under the event-forwarder goroutine; lock-protected because executeRunner
// reads it on the turn goroutine.
func (am *AgentManager) setLoopStopReason(reason string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.loopStopReason = reason
}

func (am *AgentManager) logEventF(format string, args ...interface{}) {
	am.mu.Lock()
	logger := am.logger
	am.mu.Unlock()
	if logger != nil && logger.Enabled(agentic.Warn) {
		logger.Log(agentic.Warn, format, args...)
	}
}

// LoopDetector returns the session loop detector for temporary override commands.
func (am *AgentManager) LoopDetector() *LoopDetector {
	return am.loopDetector
}

// isToolResultError returns true when a tool result indicates an execution error.
func isToolResultError(result string) bool {
	return result == "" || strings.HasPrefix(result, "Error:")
}