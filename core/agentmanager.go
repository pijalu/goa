// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"context"
	"fmt"
	"runtime"
	"sort"
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

// AgentManager manages the lifecycle of the LLM agent session. It is a thin
// facade over focused collaborators: TurnRecorder, ModeManager,
// CompanionCoordinator, AgentDrivenGate, and SteeringQueue.
type AgentManager struct {
	cfg          *config.Config
	activeAgent  *agentic.Agent
	events       chan agentic.OutputEvent
	sessionStore *SessionStore      // event recording store
	stateStore   *StateStore        // persisted mode state store
	configSaver  config.ConfigSaver // persists per-model thinking level to the home config
	// modelPersistenceSuppressed gates saving active_provider/active_model
	// (and thinking levels) to config files while a team governs the session
	// model — the team's model must never be persisted as the user's choice
	// (RC-5). Set by the team manager via SetModelPersistenceSuppressed.
	modelPersistenceSuppressed bool
	loopDetector               *LoopDetector
	eventsOut                  *event.Bus
	logger                     *agentic.Logger
	mu                         sync.Mutex
	cancel                     context.CancelFunc
	cancelGen                  int
	running                    bool
	lastUserInput              string
	systemPrompt               string
	agentBus                   *agentic.AgentBus
	mainConnector              *agentic.CommConnector
	foregroundOrch             *multiagent.ForegroundOrchestrator
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

	// lastToolNames mirrors the tool set pushed to the active agent
	// (StartSession seed + every SetTools). SetTools diffs against it to emit
	// one batched toolset-change notice to the model (bugs.md 2026-08-26).
	lastToolNames []string

	// pluginHookSink receives plugin interception points from the agent loop
	// (M2 §3.5). Wired once at boot by the app layer; nil = plugin hooks
	// disabled. The sink reads a live registry, so later plugin loads are
	// visible to already-constructed agents without any re-wiring.
	pluginHookSink agentic.PluginHookSink

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

	// lastModelMarker mirrors the most recent model_selected marker appended
	// to the session file (start + every changed switch; bugs.md 2026-08-26).
	// SetModel compares against it so repeated identical bindings stay silent.
	lastModelMarker markerPair
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
	// Seed the tool-name mirror so the FIRST SetTools diff reports only real
	// changes instead of "everything enabled".
	am.lastToolNames = toolNameList(tools)
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
	// Session-model binding (bugs.md 2026-08-26): persist WHICH provider/model
	// this session starts on, so /session restore can re-bind it later instead
	// of falling back to the ~/.goa-latest selection. The CONFIG couple is
	// recorded (not the resolved Model: registry merging rewrites Provider to
	// the canonical API family and may swap the ID for the API name — neither
	// round-trips through ProviderManager.SetActive). Written straight to the
	// store — no observer pipeline echo, no double persistence.
	providerID := firstNonEmptyString(am.cfgActiveProvider(), string(mdl.Provider))
	modelID := firstNonEmptyString(am.cfgActiveModel(), mdl.ID)
	am.recordSessionMarkerLocked(ModelMarkerSourceStart, providerID, modelID)
	return am.events, nil
}

func firstNonEmptyString(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
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

	// Only a turn that will actually START clears the per-turn accumulators.
	// Clearing before the steering check wiped the in-flight goal turn's
	// token/tool state on every user message typed mid-goal, so /stats:cache
	// lost the session's cache stats exactly while a goal ran (the finalized
	// TurnRecord came out empty).
	am.turnRecorder.ResetTurn(time.Now())

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

// IsRunning reports whether a user turn is currently in progress.
func (am *AgentManager) IsRunning() bool {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.running
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
//
// Toolset-change notice (bugs.md 2026-08-26): the previous tool set is
// diffed against the new one and ONE batched user-role message is injected
// into the conversation, so the model is always aware of tools that became
// available or unavailable (enable, disable, MCP connect/disconnect, plugin
// load/unload — every toggle funnels through here). The injection happens
// OUTSIDE am.mu: it emits events that call back into OnEvent → logEvent,
// which acquires am.mu (same discipline as InjectSystemMessage).
func (am *AgentManager) SetTools(tools []agentic.Tool) error {
	var (
		agent     *agentic.Agent
		wrapped   []agentic.Tool
		added     []string
		removed   []string
		hasChange bool
	)
	am.mu.Lock()
	if am.activeAgent == nil {
		am.mu.Unlock()
		return fmt.Errorf("no active agent session")
	}
	wrapped = am.toolsWithBus(tools)
	added, removed = diffToolNames(am.lastToolNames, toolNameList(wrapped))
	hasChange = len(added) > 0 || len(removed) > 0
	am.lastToolNames = toolNameList(wrapped)
	agent = am.activeAgent
	am.mu.Unlock()

	agent.SetTools(wrapped)
	if hasChange {
		agent.InjectUserMessage(toolsetNotice(added, removed))
	}
	return nil
}

// toolNameList extracts tool names in stable order.
func toolNameList(tools []agentic.Tool) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Schema().Name
	}
	return names
}

// diffToolNames returns which names were added and removed between the
// previous and next tool sets. Order: alphabetical for deterministic notices.
func diffToolNames(prev, next []string) (added, removed []string) {
	prevSet := make(map[string]bool, len(prev))
	for _, n := range prev {
		prevSet[n] = true
	}
	nextSet := make(map[string]bool, len(next))
	for _, n := range next {
		nextSet[n] = true
	}
	for _, n := range next {
		if !prevSet[n] {
			added = append(added, n)
		}
	}
	for _, n := range prev {
		if !nextSet[n] {
			removed = append(removed, n)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

// toolsetNotice renders the batched toolset-change notice injected as a
// user-role message. Empty when nothing changed.
func toolsetNotice(added, removed []string) string {
	if len(added) == 0 && len(removed) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[goa-tools] Toolset changed.")
	if len(added) > 0 {
		b.WriteString("\nEnabled: ")
		b.WriteString(strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		b.WriteString("\nDisabled: ")
		b.WriteString(strings.Join(removed, ", "))
	}
	b.WriteString("\nDeferred tools load on demand via tool_search (query \"select:<name>\").")
	return b.String()
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
	var marker *agentic.OutputEvent
	if am.sessionStore != nil {
		ev := am.switchMarkerLocked(
			firstNonEmptyString(am.cfgActiveProvider(), string(mdl.Provider)),
			firstNonEmptyString(am.cfgActiveModel(), mdl.ID))
		if ev != nil {
			marker = ev
		}
	}
	if am.activeAgent == nil {
		am.mu.Unlock()
		emitSessionModelMarker(am, marker)
		return
	}
	am.activeAgent.SetModel(mdl)
	compressionCfg := am.buildCompressionConfig(am.cfg, mdl.ID, mdl.ContextWindow)
	if compressionCfg.MaxTokens > 0 || am.hasCompressionOverride(mdl.ID) {
		am.activeAgent.SetContextCompression(compressionCfg)
	}
	am.mu.Unlock()
	emitSessionModelMarker(am, marker)
	am.syncThinkingLevelForActiveModel()
}

// switchMarkerLocked builds the model_selected switch marker for the couple
// when it CHANGED relative to the last recorded binding, updating the cache;
// returns nil for an unchanged couple (silent re-selection). Requires am.mu.
// Callers have already committed the new couple to the config (SetModel
// contract), so reading the config here captures the user-facing pair.
func (am *AgentManager) switchMarkerLocked(providerID, modelID string) *agentic.OutputEvent {
	pair := pairFrom(providerID, modelID)
	if am.lastModelMarker == pair {
		return nil
	}
	am.lastModelMarker = pair
	ev := ModelSelectedMarker(ModelMarkerSourceSwitch, providerID, modelID)
	return &ev
}

// recordSessionMarkerLocked unconditionally appends a binding marker (the
// session-start variant: the writer just rotated, nothing to dedupe against).
// Requires am.mu.
func (am *AgentManager) recordSessionMarkerLocked(source, providerID, modelID string) {
	if am.sessionStore == nil {
		return
	}
	am.lastModelMarker = pairFrom(providerID, modelID)
	am.sessionStore.WriteEvent(ModelSelectedMarker(source, providerID, modelID))
}

// emitSessionModelMarker performs the deferred store write outside am.mu,
// mirroring the lock discipline documented on InjectSystemMessage.
func emitSessionModelMarker(am *AgentManager, marker *agentic.OutputEvent) {
	if marker == nil || am.sessionStore == nil {
		return
	}
	am.sessionStore.WriteEvent(*marker)
}

func (am *AgentManager) cfgActiveProvider() string {
	if am.cfg == nil {
		return ""
	}
	return am.cfg.ActiveProvider
}

func (am *AgentManager) cfgActiveModel() string {
	if am.cfg == nil {
		return ""
	}
	return am.cfg.ActiveModel
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

// RefreshAutoHeal pushes the current execution.auto_heal_tool_calls value
// from the live config into the active agent's snapshot, so /config → Tools →
// "Tool call fixing" (and /config:set execution.auto_heal_tool_calls) takes
// effect on the ongoing session instead of only after a restart
// (bugs-20260826-config-tool-live-sync). Nil-safe when no session is active.
func (am *AgentManager) RefreshAutoHeal() {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.activeAgent != nil {
		am.activeAgent.SetAutoHealToolCalls(am.cfg.Execution.AutoHealToolCalls)
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
