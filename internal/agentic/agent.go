// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package agentic provides a Go SDK for building AI agents that interact with
// LLMs and execute tools. The core abstraction is the Agent, which manages
// conversation state, tool execution, and event emission.
package agentic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/hooks"
	"github.com/pijalu/goa/internal/perms"
	"github.com/pijalu/goa/internal/toolaccess"
)

// ReasoningEffort controls how much reasoning a model performs.
// Values are provider-specific (e.g., "low"/"medium"/"high" for OpenAI,
// "on"/"off" for Gemma).
type ReasoningEffort string

const (
	ReasoningEffortLow    ReasoningEffort = "low"
	ReasoningEffortMedium ReasoningEffort = "medium"
	ReasoningEffortHigh   ReasoningEffort = "high"
	ReasoningEffortXHigh  ReasoningEffort = "xhigh"
	ReasoningEffortOn     ReasoningEffort = "on"
	ReasoningEffortOff    ReasoningEffort = "off"
)

// Agent orchestrates conversations with an LLM provider, managing tool
// execution, conversation history, and event broadcasting to observers.
//
// Create an Agent using NewAgent with a Config that specifies the model,
// system prompt, tools, and optional logger.
//
// The Agent emits events via the Output channel and to registered observers.
// Use AddObserver to receive structured events for UI updates, logging, etc.
//
// Example:
//
//	agent := agentic.NewAgent(agentic.Config{
//	    Model:         myModel,
//	    StreamOptions: opts,
//	    SystemPrompt:  "You are a helpful assistant.",
//	    Tools:         []agentic.Tool{MyTool{}},
//	})
//	agent.Run(ctx, "Hello!")
type Agent struct {
	cfg       Config
	reg       ToolLookup
	history   []Message
	observers []observerEntry
	// observerCounter is a per-agent source of unique observer ids used as
	// removal handles (see AddObserver). Per-agent (not package-global) so
	// agents do not share mutable state and tests stay isolated.
	observerCounter uint64
	Output          chan Message

	mu         sync.Mutex
	processing bool
	queue      []string
	cancel     context.CancelFunc
	emitState  OutputState // Track last emitted state for state change events

	// Loop guardrail: tracks how many times each exact tool call (name + input)
	// has been issued in the current turn. Used by MaxToolRepeatTotal.
	turnToolCalls map[string]int
	// turnToolCallCount is kept for metrics/logging only. It is no longer used
	// as a hard per-turn budget cap; that cap is now based on duplicate counts
	// within a rolling window.
	turnToolCallCount int

	// thinkingBuf accumulates delta thinking tokens from the current assistant
	// response so they can be included in the assistant message when a tool call
	// is handled. DeepSeek requires reasoning_content to be passed back.
	thinkingBuf strings.Builder

	// thinkingDisplayBuf accumulates thinking tokens that have not yet been
	// displayed, suppressing raw tool-call XML that spans multiple deltas.
	// Once the buffer contains no tool-like tags, it is flushed to the UI.
	thinkingDisplayBuf strings.Builder

	// contentBuf accumulates delta content tokens from the current assistant
	// response so they can be included in the assistant message. Without this,
	// content sent before a tool call (or in a text-only response) is lost.
	contentBuf strings.Builder

	// turnStatsEmitted tracks whether the provider already sent real token
	// stats during this turn. If true, we skip emitting estimated stats at
	// turn end to avoid double-counting.
	turnStatsEmitted bool

	// turnStartHistoryLen records the length of the history at the start of
	// the current user turn. It is used to identify assistant messages that
	// belong to the current turn so that stream retries can undo only the
	// partial/corrupted message from the failing round, not assistant messages
	// from earlier rounds of the same turn. A negative value means the field
	// has not been initialized (e.g. tests that call undoLastAssistantMessage
	// directly), in which case the function falls back to the last user message.
	turnStartHistoryLen int

	// providerUsage stores the Usage from EventDone (stream_options.include_usage).
	// When set, emitTurnStats uses these real token counts instead of estimates.
	providerUsage *provider.Usage

	// genStartTime is the wall-clock time of the first streamed token in the
	// current stream. Used to compute output tok/s as a fallback when the
	// provider (LM Studio, llama.cpp, Ollama) omits timing fields.
	genStartTime time.Time
	// genDuration is the wall-clock generation time of the last completed stream
	// (first token → done), used to derive output speed when provider timings
	// are unavailable.
	genDuration time.Duration

	// contextWindow mirrors cfg.Model.ContextWindow and is updated atomically so
	// concurrent readers (e.g. effectiveMaxTokens) can read it without taking mu.
	contextWindow atomic.Int64

	// thinkingStall records when the current thinking-only phase started
	// (zero value = not in a thinking stall). Used to detect models that
	// emit reasoning tokens indefinitely without producing content or tool calls.
	// Reset whenever a content token or tool call is received.
	thinkingStallStart time.Time
	// thinkingStallWarned is set after the first stall warning is emitted
	// so we don't flood the event stream.
	thinkingStallWarned bool

	// bufferedToolCalls collects tool calls during streaming for concurrent
	// execution after the stream ends, rather than executing one at a time.
	bufferedToolCalls []provider.ContentBlock

	// budgetToolCalls records tool call IDs in the current stream that were
	// rejected because the per-turn budget or loop guardrail was exceeded.
	// These calls are still buffered (so they appear in the assistant message's
	// tool_calls array) but are NOT executed — executeBufferedToolCalls
	// substitutes the stored message for their result. Keyed by ToolCallID.
	// An entry with a non-empty string means the call was skipped with that
	// result message; empty or missing means the call was executed normally.
	budgetToolCalls map[string]string

	// lastCallKey and consecutiveCount track consecutive identical tool calls
	// (same name + same arguments) across the current turn. When a different
	// call appears (different name or args), consecutiveCount resets to 1.
	// Used for soft-repeat (2x → "already executed") and hard-repeat (3x →
	// loop guard) detection.
	lastCallKey      string
	consecutiveCount int

	// stopBatchAfterThis is set when a tool result requests that the current
	// tool batch end after this result (e.g. UpdateGoal setting a non-active
	// status). It causes completeStreamTurn to report no further tool calls
	// even if the model issued some, ending the turn after the results are
	// appended to history.
	stopBatchAfterThis bool

	// overflowRecoveryAttempted tracks whether an overflow-triggered
	// context compression + stream retry has already been attempted in
	// the current turn. Prevents infinite retry loops when compression
	// cannot free enough space. Reset at the start of each turn in
	// prepareTurn.
	overflowRecoveryAttempted bool

	// lastAssistantHash and assistantRepeatCount detect assistant-message
	// loops where the model emits the same text/thinking across consecutive
	// turns without making progress.
	lastAssistantHash    string
	assistantRepeatCount int

	// streamLoopDetected is set during streaming when the model starts
	// repeating the same substring within a single assistant block. This
	// allows a fast stop before the response grows and wastes context.
	streamLoopDetected bool

	// loopStopped is set when a hard loop guardrail fires so subsequent turns
	// are rejected instead of continuing the runaway exchange.
	loopStopped bool

	// bufferedToolCallCount is the number of tool calls buffered during the
	// current stream. It is reset once the batch is executed so the TUI can
	// render progress like "tool calling (x/Y)" across the stream/tool
	// boundary. EventToolCall consumers should not rely on this for state
	// machine logic.
	bufferedToolCallCount int

	// recentToolCalls tracks the last N tool-call keys used to detect
	// duplicate tool calls within the rolling budget window (MaxToolCalls /
	// ToolCallLimitResetWindow). It is reset at the start of each turn.
	recentToolCalls []string
}

// ContextStats holds the current context window usage of an Agent.
//
// EstimatedTokens uses a language-aware heuristic (ASCII ≈ 0.25 tokens,
// CJK ≈ 1 token) and is accurate enough for compression decisions without
// adding external dependencies.
type ContextStats struct {
	// Messages is the number of messages in the conversation history.
	Messages int
	// Characters is the total UTF-8 character count of all messages.
	Characters int
	// EstimatedTokens is a rough token count (chars / 4 for English, chars / 2 for CJK).
	EstimatedTokens int
	// MaxTokens is the configured context window limit (0 = unknown/unlimited).
	MaxTokens int
	// UsagePercent is EstimatedTokens / MaxTokens * 100 (0 if MaxTokens is 0).
	UsagePercent int
	// AutoMax is true when MaxTokens was inferred from model metadata rather
	// than an explicit user configuration.
	AutoMax bool
}

// CompressionStrategy selects the context compression algorithm.
type CompressionStrategy string

const (
	// CompressionToolElision removes tool call arguments and tool
	// results from older messages, replacing them with brief placeholders.
	// This is the cheapest strategy — no LLM round-trip required.
	CompressionToolElision CompressionStrategy = "tool_elision"

	// CompressionSummarize uses the LLM to summarize a block of
	// older messages into a single assistant message. Most aggressive.
	CompressionSummarize CompressionStrategy = "summarize"

	// CompressionSelective removes the oldest messages entirely,
	// keeping only system prompt + recent turns.
	CompressionSelective CompressionStrategy = "selective"

	// CompressionHybrid first applies tool_elision, then if still
	// over threshold, applies selective removal. Best balance.
	CompressionHybrid CompressionStrategy = "hybrid"

	// CompressionMicro replaces old tool result bodies with a short marker
	// during cache-miss turns, preserving conversation structure while
	// freeing context.
	CompressionMicro CompressionStrategy = "micro"
)

// SkillExecutionMode controls how the skill runner executes skills.
type SkillExecutionMode string

const (
	// SkillExecutionModeSubAgent runs each skill in an isolated sub-agent.
	// This is the default and provides full context isolation.
	SkillExecutionModeSubAgent SkillExecutionMode = "subagent"

	// SkillExecutionModeInline returns skill instructions as a tool result
	// within the parent LLM session. The LLM follows the instructions using
	// the parent agent's tools. Context compression is recommended.
	SkillExecutionModeInline SkillExecutionMode = "inline"
)

// ContextCompressionConfig controls automatic conversation history compression.
//
// A zero value disables automatic compression. Use this to manage context
// window limits, especially important when using inline skill execution mode.
type ContextCompressionConfig struct {
	// MaxTokens is the context window limit. When estimated tokens
	// exceed ThresholdPercent of this, compression is triggered.
	// 0 disables token-based triggering.
	MaxTokens int

	// ThresholdPercent triggers compression when usage exceeds this
	// percentage of MaxTokens. Default: 100 (compress only at limit).
	// Recommended for inline mode: 75-80.
	ThresholdPercent int

	// OnContextError triggers compression when the LLM returns a
	// context-length / token-limit error. Default: true.
	OnContextError bool

	// MicroCompaction configures the micro compaction strategy.
	// Only used when Strategy == CompressionMicro.
	MicroCompaction MicroCompactionConfig

	// Strategy selects the compression algorithm.
	// Default: CompressionToolElision.
	Strategy CompressionStrategy

	// PreserveRecentTurns keeps the last N user/assistant/tool turns
	// uncompressed. Default: 2.
	PreserveRecentTurns int
}

// Config holds the configuration for creating a new Agent.
type Config struct {
	// Model is the LLM model to use. Agent uses provider.Stream() for all
	// LLM interactions.
	Model provider.Model
	// APIKey is the API key for the model provider.
	APIKey string
	// StreamOptions configures the stream request.
	StreamOptions provider.StreamOptions

	// SystemPrompt is the initial system message sent to the LLM.
	SystemPrompt string
	// Logger is an optional leveled logger for debugging. If nil, logging is disabled.
	Logger *Logger
	// Tools is the list of tools available to the agent.
	Tools []Tool
	// SkillExecutionMode controls how the skill runner executes skills.
	// Default is SkillExecutionModeSubAgent.
	SkillExecutionMode SkillExecutionMode
	// ContextCompression controls automatic history compression.
	// Zero value disables automatic compression.
	ContextCompression ContextCompressionConfig
	// MaxToolRepeatTotal is the maximum number of identical tool calls (same
	// tool + same arguments) allowed within a single turn, including the first
	// call. When the count exceeds this threshold across any streaming rounds
	// in the turn, subsequent identical calls receive a synthetic loop-guardrail
	// result. Default: 10. Set to 0 to disable this total-count guardrail.
	MaxToolRepeatTotal int
	// MaxToolRepeatConsecutive is the maximum number of CONSECUTIVE identical
	// tool calls allowed within a single turn. When a different tool or
	// different arguments appears between calls, the consecutive counter resets.
	// Default: 2 (allow up to 2 consecutive calls; soft-repeat at 2, hard-loop
	// at 3+). Set to 0 to disable.
	MaxToolRepeatConsecutive int
	// MaxToolCalls is the maximum number of duplicate occurrences of the same
	// tool call (same tool + same arguments) allowed within the rolling window
	// of the last ToolCallLimitResetWindow calls. When the count exceeds this
	// threshold, subsequent identical calls receive a synthetic loop-guardrail
	// result telling the model to change approach or use the previous result.
	// Unique calls in the window do not count toward this limit. Default: 0
	// (no rolling-window duplicate guardrail).
	MaxToolCalls int
	// MaxStreamRounds is the maximum number of LLM stream rounds per turn.
	// After this many rounds, if the model is still making tool calls, a
	// recovery hint is injected. Set to 0 for unlimited (default).
	MaxStreamRounds int
	// DisableToolBudget when true disables the per-turn tool-call budget check
	// entirely, allowing unlimited tool calls per turn. Useful for sessions with
	// many small tool calls. Set via config or session-level toggle.
	DisableToolBudget bool
	// ToolCallLimitResetWindow is the size of the rolling window used to count
	// duplicate tool calls for MaxToolCalls. A call that falls outside this
	// window is no longer counted as a duplicate. Default: 0 (an effective
	// default of max(3*MaxToolCalls, 10) is used).
	ToolCallLimitResetWindow int
	// ReasoningEffort controls the amount of reasoning the model performs.
	// Values are provider-specific (e.g. "low"/"medium"/"high" for OpenAI,
	// "on"/"off" for Gemma). The zero value ("") omits the parameter.
	ReasoningEffort ReasoningEffort
	// ToolResultAsUser overrides whether tool results are sent as user
	// messages (with XML markers) instead of role: "tool".  When nil, the
	// provider's auto-detected compat setting is used.  Some models (e.g.
	// Gemma via LM Studio, Qwen) require this to associate results with calls.
	ToolResultAsUser *bool
	// GoalStateProvider injects goal context into the system prompt at each
	// turn boundary. Nil disables goal injection.
	GoalStateProvider GoalStateProvider
	// AutoHealToolCalls enables parsing of malformed XML tool calls emitted
	// by local models.  When true, the agent extracts <tool_call> and
	// <function=name> markup from the assistant text and treats it as a tool
	// call.  Disabled by default.
	AutoHealToolCalls bool
	// ProjectDir is the root of the codebase. It is used by SOLO mode to
	// restrict file-system and shell access to the project directory.
	ProjectDir string
	// GetAutonomy returns the current autonomy level. When non-nil and it
	// returns AutonomySolo, tool calls are validated against the SOLO policy.
	GetAutonomy func() internal.AutonomyLevel
	// GetGuardConfig returns the current mode's access-control rules. When
	// non-nil and the returned config contains rules, tool calls are validated
	// against them before execution.
	GetGuardConfig func() perms.GuardConfig

	// ConfirmTool is called before executing a tool that requires user
	// approval in ask/confirm autonomy modes. It returns true when the tool
	// is allowed to run. When nil or when the current autonomy does not
	// require confirmation, the tool runs without invoking this callback.
	ConfirmTool func(ctx context.Context, toolName, input string) (bool, error)

	// ThinkingStallWarn is the duration of pure thinking (no content or tool
	// calls) before a warning is emitted as an EventProgress. Zero means
	// the default of 60s.
	ThinkingStallWarn time.Duration
	// ThinkingStallStop is the duration of pure thinking before the stream
	// is interrupted. Zero means the default of 120s.
	ThinkingStallStop time.Duration
}

// NewAgent creates a new Agent with the given configuration.
func NewAgent(cfg Config) *Agent {
	// Apply documented micro-compaction defaults when the strategy is micro but
	// the caller left MicroCompaction at zero. Without this, DefaultMicroCompaction
	// Config's values (KeepRecentMessages=20, MinContextRatio=0.5, ...) are
	// silently never applied and microCompactForced reads zero values.
	if cfg.ContextCompression.Strategy == CompressionMicro && cfg.ContextCompression.MicroCompaction == (MicroCompactionConfig{}) {
		cfg.ContextCompression.MicroCompaction = DefaultMicroCompactionConfig
	}
	a := &Agent{
		cfg:           cfg,
		reg:           NewToolRegistry(cfg.Tools),
		Output:        make(chan Message, 10),
		turnToolCalls: make(map[string]int),
		// Negative means "not initialized yet"; undoLastAssistantMessage falls
		// back to the last user message in that case (e.g. direct test calls).
		turnStartHistoryLen: -1,
	}
	if cfg.Model.ContextWindow > 0 {
		a.contextWindow.Store(int64(cfg.Model.ContextWindow))
	}
	return a
}

// SetHistory replaces the conversation history.
// Used for session restoration on reconnect.
func (a *Agent) SetHistory(history []Message) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Ensure system prompt is preserved if not present in new history
	hasSystem := false
	for _, m := range history {
		if m.Role == System {
			hasSystem = true
			break
		}
	}

	if !hasSystem && a.cfg.SystemPrompt != "" {
		history = append([]Message{{
			Type:    Content,
			Role:    System,
			Content: a.cfg.SystemPrompt,
		}}, history...)
	}

	a.history = history
}

// SetModel replaces the active model for subsequent turns without
// rebuilding the rest of the agent configuration.
func (a *Agent) SetModel(mdl provider.Model) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Model = mdl
	if mdl.ContextWindow > 0 {
		a.contextWindow.Store(int64(mdl.ContextWindow))
	}
}

// SetContextCompression replaces the context compression configuration for
// subsequent turns. Used when the model changes mid-session so the context
// ceiling tracks the new model's context window.
func (a *Agent) SetContextCompression(cfg ContextCompressionConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.ContextCompression = cfg
}

// SetReasoningEffort replaces the reasoning-effort level for subsequent turns.
func (a *Agent) SetReasoningEffort(effort ReasoningEffort) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.ReasoningEffort = effort
}

// ReasoningEffort returns the current reasoning-effort level.
func (a *Agent) ReasoningEffort() ReasoningEffort {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.ReasoningEffort
}

// SetTools replaces the tool set available to the agent for subsequent turns.
// The updated list takes effect on the next provider call without losing the
// current conversation history.
func (a *Agent) SetTools(tools []Tool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Tools = tools
	a.reg = NewToolRegistry(tools)
}

// InjectSystemMessage appends a system message to the conversation history.
// It is sent to the model on the next turn so the model can be informed of
// runtime changes (for example newly enabled tools) without losing history.
func (a *Agent) InjectSystemMessage(content string) {
	msg := Message{Type: Content, Role: System, Content: content}
	a.mu.Lock()
	a.history = append(a.history, msg)
	a.mu.Unlock()
	a.emitMessage(msg)
}

// Model returns the active model configuration.
func (a *Agent) Model() provider.Model {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.Model
}

// StreamOptions returns the configured stream options.
func (a *Agent) StreamOptions() provider.StreamOptions {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.StreamOptions
}

// SetStreamOptions replaces the stream options for subsequent turns.
// This updates the API key, headers, timeout, transport, and other provider
// settings. Call after switching providers so the new provider's credentials
// are used on the next turn.
// SetContextWindow updates the model's advertised context window at runtime.
// Used by the host to refresh the loaded context length for local providers
// after the model has finished loading.
func (a *Agent) SetContextWindow(nCtx int) {
	if nCtx <= 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Model.ContextWindow = nCtx
	a.contextWindow.Store(int64(nCtx))
}

func (a *Agent) SetStreamOptions(opts provider.StreamOptions) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.StreamOptions = opts
	if opts.APIKey != "" {
		a.cfg.APIKey = opts.APIKey
	}
}

// GetHistory returns a copy of the conversation history.
func (a *Agent) GetHistory() []Message {
	a.mu.Lock()
	defer a.mu.Unlock()

	result := make([]Message, len(a.history))
	copy(result, a.history)
	return result
}

// observerEntry pairs an OutputObserver with a unique ID used as an identity
// handle for removal. The id is what AddObserver returns (as a remove handle);
// observer values themselves may be non-comparable function types.
type observerEntry struct {
	obs OutputObserver
	id  uint64
}

// AddObserver registers an observer to receive output events and returns a
// remove handle. Call the returned func exactly once to unregister that
// specific registration. Using a handle (instead of comparing observer values
// via reflect) makes removal reliable even when the same observer is added
// twice or the observer is wrapped in an adapter.
func (a *Agent) AddObserver(o OutputObserver) func() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.observerCounter++
	id := a.observerCounter
	a.observers = append(a.observers, observerEntry{obs: o, id: id})
	return func() { a.removeObserverByID(id) }
}

// RemoveObserver unregisters a previously added observer by value. It is kept
// for backwards compatibility; new code should prefer the remove handle
// returned by AddObserver. Comparison is identity-based (pointer equality);
// function-typed observers cannot be matched this way (comparing two non-nil
// func values panics), so callers using OutputObserverFunc must retain and use
// the AddObserver handle. RemoveObserver is a no-op when no entry matches.
func (a *Agent) RemoveObserver(o OutputObserver) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, entry := range a.observers {
		if safeObserverEqual(entry.obs, o) {
			a.observers = append(a.observers[:i], a.observers[i+1:]...)
			return
		}
	}
}

// removeObserverByID removes the observer entry with the given id (no-op if
// not found). Called by the remove handle returned from AddObserver.
func (a *Agent) removeObserverByID(id uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, entry := range a.observers {
		if entry.id == id {
			a.observers = append(a.observers[:i], a.observers[i+1:]...)
			return
		}
	}
}

// safeObserverEqual reports whether two OutputObserver values are identical by
// pointer/interface equality. Comparing two non-nil function values panics, so
// the comparison is guarded with a recover; such observers are considered
// non-matching (callers must use the AddObserver handle for them). This avoids
// any dependency on reflect.
func safeObserverEqual(a, b OutputObserver) (eq bool) {
	if a == nil || b == nil {
		return a == b
	}
	defer func() { _ = recover() }()
	return a == b
}

func (a *Agent) transitionTo(target OutputState) {
	if a.emitState != target {
		a.emitState = target
		a.emitEvent(OutputEvent{
			Type:  EventStateChange,
			State: target,
		})
	}
}

// Run starts a new conversation turn with the given user input.
// If the agent is already processing, the input is queued and handled
// after the current turn completes. The system prompt is automatically
// prepended on the first call.
//
// Run blocks until the conversation turn completes or the context is cancelled.
func (a *Agent) Run(ctx context.Context, input string) error {
	return a.RunWithMetadata(ctx, input, nil)
}

// RunWithImages starts a new conversation turn with the given user input and
// image attachments. Images are file paths; the provider layer encodes them.
func (a *Agent) RunWithImages(ctx context.Context, input string, images []string) error {
	return a.runInternal(ctx, input, images, nil)
}

// RunWithMetadata starts a new conversation turn with the given user input
// and optional metadata. Metadata is attached to the user message and propagated
// through the Output channel and to all observers, but is NOT sent to the LLM.
//
// This is useful for attaching application-level tags (e.g., category, visibility)
// to individual messages without affecting model context.
func (a *Agent) RunWithMetadata(ctx context.Context, input string, metadata map[string]string) error {
	return a.runInternal(ctx, input, nil, metadata)
}

func (a *Agent) runInternal(ctx context.Context, input string, images []string, metadata map[string]string) error {
	a.mu.Lock()

	// Initialize history with system prompt on first call
	if len(a.history) == 0 {
		sysMsg := Message{
			Type:    Content,
			Role:    System,
			Content: a.cfg.SystemPrompt,
		}
		a.history = append(a.history, sysMsg)
		a.mu.Unlock()
		a.emitMessage(sysMsg)
		a.mu.Lock()
	}

	// If processing, queue and return
	if a.processing {
		a.queue = append(a.queue, input)
		a.mu.Unlock()
		return nil
	}

	a.processing = true
	ctx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.mu.Unlock()

	// Process current and queued inputs
	currentInput := input
	var err error

	for {
		// Add user message to history and emit event
		userMsg := Message{
			Type:     Content,
			Role:     User,
			Content:  currentInput,
			Images:   images,
			Metadata: metadata,
		}
		a.history = append(a.history, userMsg)
		a.emitMessage(userMsg)

		// Process one turn
		err = a.processTurn(ctx)
		if err != nil {
			break
		}

		// Check for queued inputs
		a.mu.Lock()
		if len(a.queue) == 0 {
			a.mu.Unlock()
			break
		}
		currentInput = a.queue[0]
		a.queue = a.queue[1:]
		a.mu.Unlock()
	}

	// Cleanup on every exit path (success, error, empty queue). Mark not
	// processing and cancel the per-turn child ctx before discarding the func.
	// Without the cancel() call, every completed turn leaks the cancellable ctx
	// subtree until the *parent* ctx is cancelled (go vet -lostcancel can't see
	// this because cancel is stored in a struct field). The error path
	// previously also left a.processing==true, which made the next Run() queue
	// forever instead of processing.
	a.finishProcessing()

	return err
}

// finishProcessing marks the agent idle and cancels the per-turn child context.
// It must run on every exit path out of runInternal so that the cancellable
// turn ctx (and its subtree) is released and the agent can accept new turns.
// Holding the cancel func without calling it leaks the child ctx tree until the
// caller's parent ctx is cancelled; go vet -lostcancel cannot detect this
// because the func is stored in a struct field rather than a local.
func (a *Agent) finishProcessing() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.processing = false
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
}

// RunAndCollect runs the agent synchronously and collects all text output
// (EventContent) into a single string. Useful for callers that need the
// full response without wiring their own observer, such as sub-agent skill
// execution.
//
// The observer is automatically registered before Run and removed after.
// RunAndCollect runs the agent synchronously and collects all ASSISTANT text
// output (EventContent with Role: Assistant) into a single string.
// System prompt and user messages are excluded. Useful for callers that
// need the full response without wiring their own observer, such as
// sub-agent skill execution or companion testing.
func (a *Agent) RunAndCollect(ctx context.Context, input string) (string, error) {
	var buf strings.Builder
	obs := OutputObserverFunc(func(ev OutputEvent) {
		if ev.Type == EventContent && ev.Role == Assistant && ev.Text != "" {
			buf.WriteString(ev.Text)
		}
	})
	remove := a.AddObserver(obs)
	defer remove()
	err := a.Run(ctx, input)
	return buf.String(), err
}

// Stop cancels any ongoing processing and resets the agent state.
func (a *Agent) Stop() {
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	a.processing = false
	a.queue = nil
	a.mu.Unlock()
}

func (a *Agent) processTurn(ctx context.Context) error {
	if a.cfg.Model.ID == "" && a.cfg.Model.Api == "" {
		return fmt.Errorf("no model configured: set Config.Model")
	}
	if a.loopStopped {
		return fmt.Errorf("session stopped due to a runaway loop; please review the conversation and retry")
	}
	if err := a.processTurnWithStream(ctx); err != nil {
		return err
	}
	return a.checkProgressLoop()
}

// checkProgressLoop detects runaway conversations where the assistant repeats
// the same meaningful message across consecutive turns without progress.
// On the first repeat it injects a warning hint; on the second repeat it
// stops the session with a clear error.
func (a *Agent) checkProgressLoop() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	msg := a.lastAssistantMessage()
	if !a.isMeaningfulAssistantMessage(msg) {
		return nil
	}

	hash := a.hashAssistantMessage(msg)
	if hash != a.lastAssistantHash {
		a.lastAssistantHash = hash
		a.assistantRepeatCount = 0
		return nil
	}

	a.assistantRepeatCount++
	a.cfg.Logger.Log(Warn, "Loop guardrail: assistant message repeated %d time(s)", a.assistantRepeatCount)

	if a.assistantRepeatCount == 1 {
		hint := "[goa-system] Your last response was identical to the previous one. Progress has stalled. Change your approach: use a tool, produce different output, or stop and explain the blocker. Repeating the same text will end the session."
		a.history = append(a.history, Message{Type: Content, Role: System, Content: hint})
		return nil
	}

	a.loopStopped = true
	return fmt.Errorf("runaway loop detected: the assistant repeated the same response %d consecutive times without progress; session stopped", a.assistantRepeatCount+1)
}

// lastAssistantMessage returns the most recent assistant message in history.
func (a *Agent) lastAssistantMessage() Message {
	for i := len(a.history) - 1; i >= 0; i-- {
		if a.history[i].Role == Assistant {
			return a.history[i]
		}
	}
	return Message{}
}

// isMeaningfulAssistantMessage reports whether a message should participate in
// progress-loop detection. Any assistant turn — including an empty one with no
// tool calls — can be a stall signal, because the model is supposed to produce
// content, reasoning, or tool calls. Empty turns are treated as meaningful so
// that repeated no-op turns are caught before the context explodes.
func (a *Agent) isMeaningfulAssistantMessage(msg Message) bool {
	return msg.Role == Assistant
}

// hashAssistantMessage builds a simple fingerprint of an assistant message.
func (a *Agent) hashAssistantMessage(msg Message) string {
	return fmt.Sprintf("%s\x00%s\x00%v", strings.TrimSpace(msg.Content), strings.TrimSpace(msg.Thinking), len(msg.ToolCalls))
}

// withToolResultAsUser returns a copy of model with ToolResultAsUser set on its
// OpenAI completions compat.  Existing compat fields are preserved.
func (a *Agent) withToolResultAsUser(model provider.Model, value bool) provider.Model {
	compat, ok := model.Compat.(provider.OpenAICompletionsCompat)
	if !ok {
		compat = provider.OpenAICompletionsCompat{}
	}
	compat.ToolResultAsUser = &value
	model.Compat = compat
	return model
}

func (a *Agent) prepareTurn(ctx context.Context) (provider.Model, provider.StreamOptions, provider.Context) {
	a.mu.Lock()
	a.turnToolCalls = make(map[string]int)
	a.turnToolCallCount = 0
	a.contentBuf.Reset()
	a.thinkingBuf.Reset()
	a.thinkingDisplayBuf.Reset()
	a.turnStatsEmitted = false
	a.turnStartHistoryLen = len(a.history)
	a.bufferedToolCalls = nil
	a.bufferedToolCallCount = 0
	a.budgetToolCalls = make(map[string]string)
	a.stopBatchAfterThis = false
	a.providerUsage = nil
	a.recentToolCalls = nil
	a.lastCallKey = ""
	a.consecutiveCount = 0
	a.streamLoopDetected = false
	a.overflowRecoveryAttempted = false
	a.mu.Unlock()

	if err := a.maybeCompress(ctx); err != nil {
		a.cfg.Logger.Log(Error, "proactive compression failed: %v", err)
	}
	a.enforceContextCeiling()

	pCtx := a.buildProviderContext(ctx)

	model := a.cfg.Model
	if a.cfg.ToolResultAsUser != nil {
		model = a.withToolResultAsUser(model, *a.cfg.ToolResultAsUser)
	}

	opts := a.cfg.StreamOptions
	if opts.APIKey == "" && a.cfg.APIKey != "" {
		opts.APIKey = a.cfg.APIKey
	}

	return model, opts, pCtx
}

// formatRetryMessage turns a stream error into a concise user-facing
// message that includes the HTTP status, provider message, and error code
// when available.
func formatRetryMessage(err error) string {
	var respErr provider.HTTPResponseError
	if errors.As(err, &respErr) {
		status := respErr.StatusCode()
		body := respErr.ResponseBody()
		var parsed struct {
			Error struct {
				Message string `json:"message"`
				Code    string `json:"code"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		msg := ""
		code := ""
		if json.Unmarshal([]byte(body), &parsed) == nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
			code = parsed.Error.Code
		}
		if msg == "" {
			msg = body
		}
		if code != "" {
			return fmt.Sprintf("Error: %d - %s (%s) - retrying", status, msg, code)
		}
		return fmt.Sprintf("Error: %d - %s - retrying", status, msg)
	}
	return fmt.Sprintf("Error: %s - retrying", err.Error())
}

// handleStreamFailure handles a stream error, retrying when appropriate.
// Returns true if the failure was fully handled (caller should return retErr).
func (a *Agent) handleStreamFailure(ctx context.Context, streamErr error, model provider.Model, opts provider.StreamOptions) (handled bool, retErr error) {
	a.cfg.Logger.Log(Warn, "stream failure: %v", streamErr)
	// Reset per-round buffers so a retry starts with a clean state. Then undo
	// any assistant message that was appended in the failing round (if any).
	// Hold mu for both operations since they share state.
	a.mu.Lock()
	a.resetStreamRoundState()
	a.mu.Unlock()
	a.undoLastAssistantMessage()

	if errors.Is(streamErr, context.Canceled) {
		return true, streamErr
	}

	// Overflow guard: only one compress+retry per turn.  If compression
	// cannot free enough space, the second overflow kills the turn with
	// a clear error instead of retrying into an infinite loop.
	if isContextLengthError(streamErr) {
		if a.overflowRecoveryAttempted {
			a.cfg.Logger.Log(Error, "Overflow recovery failed after compress+retry — giving up")
			a.emitEvent(OutputEvent{Type: EventProgress, Text: "Context overflow recovery failed — compress+retry cycle exhausted. The conversation is too long for this model's context window."})
			return true, fmt.Errorf("context overflow: compression freed insufficient space after retry; try a larger context window model or reset the session")
		}
		a.overflowRecoveryAttempted = true
		a.cfg.Logger.Log(Info, "Overflow recovery: compressing context and retrying once")
	}

	a.cfg.Logger.Log(Warn, "stream error, retrying: %v", streamErr)

	// Surface the failure as a system chat bubble so the user can see the
	// retry in the conversation history, not just a transient status message.
	a.emitEvent(OutputEvent{
		Type:     EventContent,
		Role:     System,
		Text:     formatRetryMessage(streamErr),
		Metadata: map[string]string{"category": "system-notification"},
	})

	toolCallEncountered, retried := a.retryStream(ctx, streamErr, model, opts)
	if retried {
		if !toolCallEncountered {
			return true, nil
		}
		return false, nil
	}

	a.emitEvent(OutputEvent{Type: EventProgress, Text: ""})
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	return true, fmt.Errorf("LLM connection lost after retries: %w", streamErr)
}

// retryStream attempts to reconnect up to two times after a stream error.
// Returns whether any retry succeeded and whether a tool call was encountered.
// On context cancellation the function returns promptly instead of sleeping
// through the full backoff window.
func (a *Agent) retryStream(ctx context.Context, originalErr error, model provider.Model, opts provider.StreamOptions) (toolCallEncountered bool, retried bool) {
	var streamErr error
	for retry := 0; retry < 2; retry++ {
		a.cfg.Logger.Log(Info, "retry attempt %d after stream error", retry+1)
		a.emitEvent(OutputEvent{Type: EventProgress, Text: fmt.Sprintf("Reconnecting (attempt %d/2)...", retry+1)})

		// Sleep with context awareness so Ctrl+C isn't ignored during backoff.
		select {
		case <-time.After(time.Duration(retry+1) * time.Second):
		case <-ctx.Done():
			return false, false
		}

		pCtx := a.buildProviderContext(ctx)
		stream, err := provider.Stream(model, pCtx, opts)
		if err != nil {
			a.cfg.Logger.Log(Warn, "retry stream failed: %v", err)
			continue
		}
		toolCallEncountered, streamErr = a.consumeStream(ctx, stream)
		if streamErr == nil {
			a.emitEvent(OutputEvent{Type: EventProgress, Text: ""})
			return toolCallEncountered, true
		}
		// Clean up after the failed retry so the next attempt (or error path)
		// does not inherit partial tokens, buffered tool calls, or a spurious
		// assistant message.
		a.mu.Lock()
		a.resetStreamRoundState()
		a.mu.Unlock()
		a.undoLastAssistantMessage()
		a.cfg.Logger.Log(Warn, "retry attempt %d also failed: %v", retry+1, streamErr)
	}
	return false, false
}

func (a *Agent) buildProviderContext(ctx context.Context) provider.Context {
	a.mu.Lock()
	msgs := make([]provider.Message, 0, len(a.history))
	for i, m := range a.history {
		// Skip only the initial system prompt message; the provider context
		// carries it separately via SystemPrompt. Later system messages (for
		// example runtime tool-change notifications) must still be sent.
		if i == 0 && a.cfg.SystemPrompt != "" && m.Role == System {
			continue
		}
		msgs = append(msgs, migrateMessage(m))
	}
	a.mu.Unlock()

	sp := a.cfg.SystemPrompt
	if p := a.cfg.GoalStateProvider; p != nil {
		if reminder := p.ActiveGoalReminder(); reminder != "" {
			sp = reminder + "\n\n" + sp
		}
	}

	return provider.Context{
		Context:      ctx,
		SystemPrompt: sp,
		Messages:     msgs,
		Tools:        migrateSchemas(a.reg.Schemas()),
	}
}

// logProviderContext writes a concise summary of the context to the debug log.
// This makes it possible to verify that tool calls and tool results are being
// passed back to the LLM correctly.
func (a *Agent) logProviderContext(ctx provider.Context, attempt int) {
	a.cfg.Logger.Log(Debug, "Provider context (attempt %d): %d messages", attempt, len(ctx.Messages))
	for i, m := range ctx.Messages {
		a.logProviderMessage(i, m)
	}
}

func (a *Agent) logProviderMessage(i int, m provider.Message) {
	switch m.Role {
	case provider.RoleAssistant:
		toolCount := countToolCallBlocks(m.Content)
		a.cfg.Logger.Log(Debug, "  [%d] assistant content=%q tool_calls=%d", i, extractTextFromBlocks(m.Content), toolCount)
	case provider.RoleToolResult:
		toolID, toolName := extractToolResultIdentity(m.Content)
		a.cfg.Logger.Log(Debug, "  [%d] tool_result id=%s name=%s text_len=%d", i, toolID, toolName, len(extractTextFromBlocks(m.Content)))
	case provider.RoleUser:
		a.cfg.Logger.Log(Debug, "  [%d] user content_len=%d", i, len(extractTextFromBlocks(m.Content)))
	}
}

func countToolCallBlocks(blocks []provider.ContentBlock) int {
	count := 0
	for _, b := range blocks {
		if b.Type == provider.ContentBlockToolCall {
			count++
		}
	}
	return count
}

func extractToolResultIdentity(blocks []provider.ContentBlock) (id, name string) {
	for _, b := range blocks {
		if b.Type == provider.ContentBlockToolResult {
			return b.ToolCallID, b.ToolName
		}
	}
	return "", ""
}

func extractTextFromBlocks(blocks []provider.ContentBlock) string {
	var text string
	for _, b := range blocks {
		if b.Type == provider.ContentBlockText {
			text += b.Text
		}
	}
	return text
}

// undoLastAssistantMessage removes the most recent assistant message that
// was added during the current turn. Used after a stream error to retry
// without the partial/corrupted assistant turn polluting the context.
//
// turnStartHistoryLen records the history length at the start of the user
// turn, so this only removes assistant messages appended in the current
// turn and preserves assistant messages from earlier turns (e.g. tool-call
// rounds that completed successfully before the failing re-stream).
func (a *Agent) undoLastAssistantMessage() {
	a.mu.Lock()
	defer a.mu.Unlock()

	start := a.turnStartHistoryLen
	if start < 0 {
		start = 0
		for i := len(a.history) - 1; i >= 0; i-- {
			if a.history[i].Role == User {
				start = i + 1
				break
			}
		}
	}

	for i := len(a.history) - 1; i >= start; i-- {
		if a.history[i].Role == Assistant {
			a.history = a.history[:i]
			return
		}
	}
}

// consumeStream reads events from a stream, buffers tool calls, and
// executes them concurrently after the stream ends.
// Returns true if tool calls were encountered (caller should re-stream).
func (a *Agent) appendAssistantToolCallMessage(tcs []provider.ContentBlock) {
	assistantMsg := a.synthesizeAssistantBuffer()
	assistantMsg.ToolCalls = make([]ToolCallInfo, len(tcs))
	for i, tc := range tcs {
		assistantMsg.ToolCalls[i] = ToolCallInfo{
			ID: tc.ToolCallID, Type: "function",
			Name: tc.ToolName, Arguments: tc.ToolArguments,
		}
	}
	a.mu.Lock()
	a.history = append(a.history, assistantMsg)
	a.mu.Unlock()
}

func (a *Agent) scheduleAndRunToolCalls(ctx context.Context, tcs []provider.ContentBlock) []ToolCallResult {
	sched := NewToolScheduler(ctx)
	defer sched.Shutdown()
	for i := range tcs {
		tc := tcs[i]
		if a.budgetToolCalls[tc.ToolCallID] != "" {
			continue
		}
		sched.Add(a.newToolCallTask(tc))
	}
	return sched.Collect()
}

func (a *Agent) newToolCallTask(tc provider.ContentBlock) *ToolCallTask {
	return &ToolCallTask{
		Name:   tc.ToolName,
		Input:  tc.ToolArguments,
		CallID: tc.ToolCallID,
		Access: a.resolveToolAccess(tc.ToolName, tc.ToolArguments),
		Execute: func(ctx context.Context) (ToolResult, error) {
			return a.executeToolWithResult(ctx, tc.ToolName, tc.ToolArguments)
		},
	}
}

func indexResultsByID(results []ToolCallResult) map[string]ToolCallResult {
	byID := make(map[string]ToolCallResult, len(results))
	for _, r := range results {
		byID[r.CallID] = r
	}
	return byID
}

func (a *Agent) appendToolResults(tcs []provider.ContentBlock, realResults []ToolCallResult) {
	byID := indexResultsByID(realResults)
	for _, tc := range tcs {
		content := a.resolveToolResultContent(tc, byID)
		toolResult := Message{
			Type: Content, Role: ToolRole, Content: content,
			ToolName: tc.ToolName, ToolCallID: tc.ToolCallID,
		}
		a.mu.Lock()
		a.history = append(a.history, toolResult)
		a.mu.Unlock()

		if a.budgetToolCalls[tc.ToolCallID] == "" {
			a.emitEvent(OutputEvent{
				Type: EventToolResult, State: StateToolResult,
				ToolName: tc.ToolName, ToolResult: content, Text: content,
				ToolCallID: tc.ToolCallID,
			})
		}
	}
}

func (a *Agent) resolveToolResultContent(tc provider.ContentBlock, byID map[string]ToolCallResult) string {
	if msg := a.budgetToolCalls[tc.ToolCallID]; msg != "" {
		return msg
	}
	r := byID[tc.ToolCallID]
	if r.StopTurn {
		a.stopBatchAfterThis = true
	}
	if r.Err != nil {
		return fmt.Sprintf("Error: %v", r.Err)
	}
	output := r.Output
	if limit := a.toolResultSizeLimit(); limit > 0 && len(output) > limit {
		truncated := output[:limit]
		return fmt.Sprintf("%s\n[goa-system] Tool result was truncated to %d bytes (original %d bytes). The read succeeded but the result is limited to fit the available context; use a narrower query, smaller line range, or filters to see more.", truncated, limit, len(output))
	}
	return output
}

// toolResultSizeLimit returns a heuristic byte limit for a single tool result.
// If a result exceeds this, it is truncated with a clear notice so the LLM can
// adapt and the turn can continue without blowing the context window.
func (a *Agent) toolResultSizeLimit() int {
	maxTokens := a.cfg.ContextCompression.MaxTokens
	if maxTokens <= 0 {
		// No context window configured: use default tool-output cap.
		return 50000
	}
	// Reserve 1/4 of the configured context window for one tool result.
	return maxTokens / 4
}

// resolveToolAccess resolves the resource access for a tool call.
func (a *Agent) resolveToolAccess(name, input string) toolaccess.Access {
	t, ok := a.reg.Get(name)
	if !ok {
		return toolaccess.Access{}
	}
	if acc, ok := t.(toolaccess.Accessor); ok {
		return acc.Access(input)
	}
	return toolaccess.Access{}
}

// executeToolWithResult executes a tool and preserves control signals such as
// StopTurn. The turn ctx is forwarded to tools that implement ContextTool so
// long-running/hung tools can be cancelled. Tools implementing ResultTool are
// called directly; otherwise the string output of Execute is wrapped into a
// ToolResult.
func (a *Agent) enforceSoloPolicy(name, input string) error {
	if a.cfg.GetAutonomy == nil || a.cfg.ProjectDir == "" {
		return nil
	}
	if a.cfg.GetAutonomy() != internal.AutonomySolo {
		return nil
	}
	guard := perms.NewSoloGuard(a.cfg.ProjectDir)
	return guard.Validate(name, input)
}

func (a *Agent) enforceGuardPolicy(name, input string) error {
	if a.cfg.GetGuardConfig == nil {
		return nil
	}
	cfg := a.cfg.GetGuardConfig()
	if len(cfg.Rules) == 0 {
		return nil
	}
	guard := perms.NewAccessGuard(cfg)
	return guard.Validate(name, input)
}

// confirmToolIfNeeded asks for user approval when the current autonomy level
// and the tool's target paths require it. It returns an error when the call
// should be rejected (denied or confirmation failed).
func (a *Agent) confirmToolIfNeeded(ctx context.Context, name, input string) error {
	if a.cfg.ConfirmTool == nil {
		return nil
	}
	autonomy := internal.AutonomyYolo
	if a.cfg.GetAutonomy != nil {
		autonomy = a.cfg.GetAutonomy()
	}
	// SOLO and YOLO do not use the confirmation callback; SOLO is handled by
	// enforceSoloPolicy and YOLO allows everything.
	if autonomy == internal.AutonomySolo || autonomy == internal.AutonomyYolo {
		return nil
	}

	policy := perms.PathPolicy{ProjectDir: a.cfg.ProjectDir, Autonomy: string(autonomy)}
	if policy.Decide(name, input) != perms.PathAsk {
		return nil
	}

	allowed, err := a.cfg.ConfirmTool(ctx, name, input)
	if err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("tool %q was not approved", name)
	}
	return nil
}

func (a *Agent) executeToolWithResult(ctx context.Context, name, input string) (ToolResult, error) {
	if err := a.enforceGuardPolicy(name, input); err != nil {
		return ToolResult{}, err
	}
	if err := a.enforceSoloPolicy(name, input); err != nil {
		return ToolResult{}, err
	}
	if err := a.confirmToolIfNeeded(ctx, name, input); err != nil {
		return ToolResult{}, err
	}
	tool, ok := a.reg.Get(name)
	if !ok {
		return ToolResult{}, fmt.Errorf("unknown tool: %s", name)
	}
	// ContextTool takes priority: it lets the tool observe cancellation.
	if ct, ok := tool.(ContextTool); ok {
		out, err := ct.ExecuteContext(ctx, input)
		return ToolResult{Output: out, Error: err}, err
	}
	if rt, ok := tool.(ResultTool); ok {
		return rt.ExecuteWithResult(input)
	}
	out, err := tool.Execute(input)
	return ToolResult{Output: out, Error: err}, err
}

// migrateMessage converts an old-style Message to the new provider.Message format.
func migrateMessage(m Message) provider.Message {
	blocks := []provider.ContentBlock{}
	// For assistant messages that issued tool calls, OpenAI-compatible APIs
	// require the tool_call blocks to appear before the text content block.
	if m.Role == Assistant && len(m.ToolCalls) > 0 {
		for _, tc := range m.ToolCalls {
			blocks = append(blocks, provider.ContentBlock{
				Type:          provider.ContentBlockToolCall,
				ToolCallID:    tc.ID,
				ToolName:      tc.Name,
				ToolArguments: tc.Arguments,
			})
		}
	}
	blocks = append(blocks, provider.ContentBlock{
		Type: provider.ContentBlockText, Text: m.Content,
	})
	for _, path := range m.Images {
		blocks = append(blocks, provider.ContentBlock{
			Type:      provider.ContentBlockImage,
			ImageData: path,
		})
	}
	if m.Thinking != "" {
		blocks = append(blocks, provider.ContentBlock{
			Type: provider.ContentBlockThinking, Thinking: m.Thinking,
		})
	}
	// Preserve tool call identity so the provider can format tool results
	// correctly (e.g. Gemma/Qwen need tool_call_id and tool_name).
	if m.Role == ToolRole {
		blocks = append(blocks, provider.ContentBlock{
			Type:       provider.ContentBlockToolResult,
			ToolCallID: m.ToolCallID,
			ToolName:   m.ToolName,
			Text:       m.Content,
		})
	}
	return provider.Message{
		Role:    roleToProviderRole(m.Role),
		Content: blocks,
	}
}

func migrateMessages(msgs []Message) []provider.Message {
	result := make([]provider.Message, len(msgs))
	for i, m := range msgs {
		result[i] = migrateMessage(m)
	}
	return result
}

func roleToProviderRole(r Role) provider.Role {
	switch r {
	case System:
		return provider.RoleSystem
	case User:
		return provider.RoleUser
	case Assistant:
		return provider.RoleAssistant
	case ToolRole:
		return provider.RoleToolResult
	default:
		return provider.RoleUser
	}
}

// migrateSchemas converts old ToolSchema slices to provider.ToolSchema slices.
func migrateSchemas(schemas []ToolSchema) []provider.ToolSchema {
	result := make([]provider.ToolSchema, len(schemas))
	for i, s := range schemas {
		result[i] = provider.ToolSchema{
			Name:        s.Name,
			Description: s.Description,
			InputSchema: s.Schema,
		}
	}
	return result
}

// markGenStart records the wall-clock time of the first streamed token for
// the current stream, if not already recorded. Used to compute output tok/s as
// a fallback for providers that omit timing fields (LM Studio, llama.cpp, Ollama).
func (a *Agent) markGenStart() {
	if a.genStartTime.IsZero() {
		a.genStartTime = time.Now()
	}
}

// recordGenDuration captures the wall-clock generation time of the stream that
// just ended (first token → done). Stored for emitTurnStats to derive speed.
func (a *Agent) recordGenDuration() {
	if !a.genStartTime.IsZero() {
		a.genDuration = time.Since(a.genStartTime)
	}
}

// fallbackOutputSpeed returns an estimated output tok/s derived from wall-clock
// generation time. Returns 0 if no generation timing was captured. This is used
// when the provider's usage object carries no timing fields (common for local
// OpenAI-compatible servers like LM Studio, llama.cpp, and Ollama).
func (a *Agent) fallbackOutputSpeed(outputTokens int) float64 {
	if a.genDuration > 0 && outputTokens > 0 {
		if secs := a.genDuration.Seconds(); secs > 0 {
			return float64(outputTokens) / secs
		}
	}
	return 0
}

// emitTurnStats emits estimated token statistics and context usage at the
// end of a turn, but only if the provider did not already emit real stats.
func (a *Agent) emitTurnStats() {
	if a.turnStatsEmitted {
		stats := a.computeContextStats()
		a.emitEvent(OutputEvent{Type: EventContextStats, ContextStats: &stats})
		return
	}

	// If we have provider Usage from stream_options.include_usage, use it.
	// This gives accurate token counts (and cache stats) from the provider
	// instead of character-based estimates.
	a.mu.Lock()
	pu := a.providerUsage
	a.mu.Unlock()
	if pu != nil {
		if pu.InputTokens > 0 || pu.OutputTokens > 0 || pu.CacheReadTokens > 0 {
			a.emitEvent(OutputEvent{
				Type: EventTokenStats,
				Timings: &TokenTimings{
					PromptN:            pu.InputTokens,
					PredictedN:         pu.OutputTokens,
					CacheReadTokens:    pu.CacheReadTokens,
					CacheWriteTokens:   pu.CacheCreationTokens,
					PredictedPerSecond: a.fallbackOutputSpeed(pu.OutputTokens),
				},
			})
			stats := a.computeContextStats()
			a.emitEvent(OutputEvent{Type: EventContextStats, ContextStats: &stats})
			return
		}
	}

	hist := a.copyHistory()
	if len(hist) == 0 {
		return
	}

	promptTokens, predictedTokens := estimateTurnTokens(hist)

	a.emitEvent(OutputEvent{
		Type: EventTokenStats,
		Timings: &TokenTimings{
			PromptN:            promptTokens,
			PredictedN:         predictedTokens,
			PredictedPerSecond: a.fallbackOutputSpeed(predictedTokens),
		},
	})

	stats := a.computeContextStats()
	a.emitEvent(OutputEvent{Type: EventContextStats, ContextStats: &stats})
	a.turnStatsEmitted = true
}

func (a *Agent) copyHistory() []Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	hist := make([]Message, len(a.history))
	copy(hist, a.history)
	return hist
}

func estimateTurnTokens(hist []Message) (promptTokens, predictedTokens int) {
	last := findLastAssistant(hist)
	if last == nil {
		return estimateTokensFromHistory(hist), 0
	}
	predictedTokens = messageTokenCount(last)
	promptTokens = tokensBefore(hist, last)
	return
}

func findLastAssistant(hist []Message) *Message {
	for i := len(hist) - 1; i >= 0; i-- {
		if hist[i].Role == Assistant {
			return &hist[i]
		}
	}
	return nil
}

func messageTokenCount(msg *Message) int {
	total := estimateTokens(msg.Content) + estimateTokens(msg.Thinking)
	for _, tc := range msg.ToolCalls {
		total += estimateTokens(tc.Arguments)
	}
	return total
}

func tokensBefore(hist []Message, assistant *Message) int {
	var total int
	for i := range hist {
		if &hist[i] == assistant {
			break
		}
		total += estimateTokens(hist[i].Content)
		total += estimateTokens(hist[i].Thinking)
		for _, tc := range hist[i].ToolCalls {
			total += estimateTokens(tc.Arguments)
		}
	}
	return total
}

// Clear resets the conversation history and cancels any processing.
// Emits an EventClear to all observers.
func (a *Agent) Clear() {
	a.mu.Lock()

	if a.cancel != nil {
		a.cancel()
	}

	a.history = nil
	a.queue = nil
	a.processing = false
	a.mu.Unlock()

	a.emitEvent(OutputEvent{Type: EventClear})
}

// Compact summarizes the conversation history using the LLM provider
// and replaces it with a condensed version. This is useful for managing
// context window limits in long conversations.
//
// Emits an EventCompact with the summary text.
func (a *Agent) Compact(ctx context.Context) error {
	if len(a.history) == 0 {
		return nil
	}

	summary, err := a.summarizeHistory(ctx)
	if err != nil {
		return err
	}

	a.mu.Lock()
	a.history = []Message{
		{Type: Content, Role: System, Content: a.cfg.SystemPrompt},
		{Type: Content, Role: Assistant, Content: summary},
	}
	a.mu.Unlock()

	a.emitEvent(OutputEvent{Type: EventCompact, Text: summary})
	return nil
}

func (a *Agent) summarizeHistory(ctx context.Context) (string, error) {
	var msgs []Message
	for _, m := range a.history {
		if m.Role != System {
			msgs = append(msgs, m)
		}
	}

	if len(msgs) == 0 {
		return "", nil
	}

	// Use the stream-based path for summarization
	pCtx := provider.Context{
		Context:      ctx,
		SystemPrompt: "Summarize the following conversation concisely, preserving key facts and context:",
		Messages:     migrateMessages(msgs),
	}

	model := a.cfg.Model
	opts := a.cfg.StreamOptions
	if opts.APIKey == "" && a.cfg.APIKey != "" {
		opts.APIKey = a.cfg.APIKey
	}

	stream, err := provider.Stream(model, pCtx, opts)
	if err != nil {
		return "", fmt.Errorf("summarization stream: %w", err)
	}

	var summary strings.Builder
	for event := range stream.Seq() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if event.Type == provider.EventTextDelta {
			summary.WriteString(event.Delta)
		}
		if event.Type == provider.EventError {
			return "", fmt.Errorf("summarization error: %v", event.Error)
		}
	}

	if err := stream.Err(); err != nil {
		return "", fmt.Errorf("summarization failed: %w", err)
	}

	return summary.String(), nil
}

// --- Context Compression ---

// ContextStats returns the current context window usage statistics.
func (a *Agent) ContextStats() ContextStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.computeContextStats()
}

func (a *Agent) computeContextStats() ContextStats {
	var chars int
	for _, m := range a.history {
		chars += len(m.Content)
		chars += len(m.Thinking)
		for _, tc := range m.ToolCalls {
			chars += len(tc.Arguments)
		}
	}

	estimated := estimateTokensFromHistory(a.history)
	maxTokens := a.cfg.ContextCompression.MaxTokens
	autoMax := false
	if maxTokens == 0 {
		// Fall back to the model's advertised context window so the UI can
		// show usage even when the user has not configured compression.
		maxTokens = a.cfg.Model.ContextWindow
		autoMax = maxTokens > 0
	} else if a.cfg.Model.ContextWindow > maxTokens {
		// Compression is configured with a smaller limit than the model's
		// actual context window. The smaller limit still drives proactive
		// compression (see maybeCompress), but the displayed total should
		// reflect what the model can actually hold. Mark as auto so the UI
		// hints that the value comes from model metadata.
		maxTokens = a.cfg.Model.ContextWindow
		autoMax = true
	}
	usagePercent := 0
	if maxTokens > 0 {
		usagePercent = estimated * 100 / maxTokens
	}

	return ContextStats{
		Messages:        len(a.history),
		Characters:      chars,
		EstimatedTokens: estimated,
		MaxTokens:       maxTokens,
		UsagePercent:    usagePercent,
		AutoMax:         autoMax,
	}
}

// estimateTokensFromHistory returns a rough token count for a message slice
// using a language-aware heuristic: CJK ≈ 1 token, ASCII ≈ 0.25 tokens.
func estimateTokensFromHistory(msgs []Message) int {
	var total int
	for _, m := range msgs {
		total += estimateTokens(m.Content)
		total += estimateTokens(m.Thinking)
		for _, tc := range m.ToolCalls {
			total += estimateTokens(tc.Arguments)
		}
	}
	return total
}

func estimateTokens(text string) int {
	cjkCount := 0
	asciiCount := 0
	for _, r := range text {
		switch {
		case r >= '\u4e00' && r <= '\u9fff',
			r >= '\u3040' && r <= '\u309f',
			r >= '\u30a0' && r <= '\u30ff',
			r >= '\uac00' && r <= '\ud7af':
			cjkCount++
		case r < 128:
			asciiCount++
		}
	}
	others := len([]rune(text)) - cjkCount - asciiCount
	return cjkCount + asciiCount/4 + others/2
}

// MaybeCompress manually triggers context compression regardless of thresholds.
// Returns the compression result. No-op if the context is empty.
func (a *Agent) MaybeCompress(ctx context.Context) error {
	return a.MaybeCompressWith(ctx, a.cfg.ContextCompression.Strategy, true)
}

// MaybeCompressWith manually triggers context compression using the given
// strategy (empty falls back to configured). When force is true, internal
// per-strategy thresholds are bypassed so manual invocations always perform
// work. No-op if the history is empty.
func (a *Agent) MaybeCompressWith(ctx context.Context, strategy CompressionStrategy, force bool) error {
	if a == nil || len(a.history) == 0 {
		return nil
	}
	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "Manual context compression triggered (strategy=%s force=%v)", strategy, force)
	}
	return a.compressHistoryWith(ctx, strategy, force)
}

// maybeCompress checks context usage and triggers compression if needed.
func (a *Agent) maybeCompress(ctx context.Context) error {
	cfg := a.cfg.ContextCompression
	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = a.cfg.Model.ContextWindow
	}
	if maxTokens == 0 {
		return nil
	}

	strategy := cfg.Strategy
	if strategy == "" {
		strategy = CompressionToolElision
	}

	// Micro compaction uses its own internal threshold checks.
	if strategy != CompressionMicro {
		threshold := cfg.ThresholdPercent
		if threshold == 0 {
			threshold = 90
		}
		stats := a.computeContextStatsForMax(maxTokens)
		if stats.UsagePercent < threshold {
			return nil
		}

		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Info, "Context compression triggered: %d%% usage (%d / %d tokens)",
				stats.UsagePercent, stats.EstimatedTokens, stats.MaxTokens)
		}
	}

	if err := a.compressHistory(ctx); err != nil {
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Error, "Context compression failed: %v", err)
		}
		return err
	}

	// Emit stats after compression
	newStats := a.computeContextStats()
	a.emitEvent(OutputEvent{
		Type:         EventContextStats,
		ContextStats: &newStats,
	})

	return nil
}

// compressHistory applies the configured compression strategy.
func (a *Agent) compressHistory(ctx context.Context) error {
	return a.compressHistoryWith(ctx, a.cfg.ContextCompression.Strategy, false)
}

// compressHistoryWith applies a specific strategy. When force is true,
// strategies with their own internal thresholds (micro compaction) bypass
// those thresholds so that a manual /compress invocation always does
// something visible, even when usage is below the configured ratio.
// An empty strategy falls back to the configured one, then to tool_elision.
func (a *Agent) compressHistoryWith(ctx context.Context, strategy CompressionStrategy, force bool) error {
	if strategy == "" {
		strategy = a.cfg.ContextCompression.Strategy
	}
	if strategy == "" {
		strategy = CompressionToolElision
	}

	switch strategy {
	case CompressionToolElision:
		a.compressToolElision(force)
	case CompressionSelective:
		a.compressSelective()
	case CompressionSummarize:
		return a.Compact(ctx)
	case CompressionHybrid:
		return a.compressHybrid(ctx)
	case CompressionMicro:
		a.microCompactForced(force)
	default:
		a.compressToolElision(force)
	}
	return nil
}

// compressHybrid applies tool_elision then selective if still over threshold.
func (a *Agent) compressHybrid(ctx context.Context) error {
	a.compressToolElision(true)

	stats := a.computeContextStats()
	threshold := a.cfg.ContextCompression.ThresholdPercent
	if threshold == 0 {
		threshold = 100
	}
	if stats.UsagePercent < threshold {
		return nil
	}

	a.compressSelective()

	stats = a.computeContextStats()
	if stats.UsagePercent < threshold {
		return nil
	}

	return a.Compact(ctx)
}

// compressToolElision replaces old tool arguments and results with placeholders.
// When force is true (manual /compress invocation), the recent-turn preserve
// window is reduced so that small histories still have messages to elide.
func (a *Agent) compressToolElision(force bool) {
	preserve := a.cfg.ContextCompression.PreserveRecentTurns
	if preserve == 0 {
		preserve = 2
	}
	boundary := computeElisionBoundary(len(a.history), preserve)
	// Forced compression must always do visible work. If the standard boundary
	// leaves nothing to elide, keep only the two most recent messages and
	// process everything before them.
	if force && boundary <= 1 {
		boundary = len(a.history) - 2
		if boundary < 1 {
			boundary = 1
		}
	}
	a.elideToolMessages(boundary)
	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "Applied tool_elision to messages before index %d", boundary)
	}
}

func computeElisionBoundary(histLen, preserve int) int {
	boundary := histLen - preserve*3
	if boundary < 1 {
		boundary = 1
	}
	return boundary
}

func (a *Agent) elideToolMessages(boundary int) {
	for i := 1; i < boundary && i < len(a.history); i++ {
		msg := &a.history[i]
		switch msg.Role {
		case Assistant:
			if len(msg.ToolCalls) > 0 {
				for j := range msg.ToolCalls {
					msg.ToolCalls[j].Arguments = "[elided]"
				}
			}
		case ToolRole:
			// Always replace the tool result body with a compact placeholder,
			// regardless of size, so tool_elision consistently frees tokens.
			msg.Content = "[tool result elided]"
		}
	}
}

// compressSelective drops oldest messages, keeping system + recent turns.
func (a *Agent) compressSelective() {
	preserve := a.cfg.ContextCompression.PreserveRecentTurns
	if preserve == 0 {
		preserve = 2
	}

	var newHistory []Message
	if len(a.history) > 0 && a.history[0].Role == System {
		newHistory = append(newHistory, a.history[0])
	}

	boundary := findCompressionBoundary(a.history, preserve)
	newHistory = append(newHistory, a.history[boundary:]...)

	removed := len(a.history) - len(newHistory)
	a.history = newHistory

	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "Applied selective compression: removed %d messages", removed)
	}
}

// findCompressionBoundary finds the oldest message index to keep, ensuring
// tool call chains are never split.
func findCompressionBoundary(history []Message, preserve int) int {
	turnsKept := 0
	boundary := len(history)
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == User {
			turnsKept++
			if turnsKept >= preserve {
				boundary = i
				break
			}
		}
	}

	// Ensure we don't split tool call chains.
	boundary = widenBoundaryForChains(history, boundary)
	return boundary
}

func widenBoundaryForChains(history []Message, boundary int) int {
	for boundary > 1 {
		prevIdx := boundary - 1
		prevRole := history[prevIdx].Role

		if prevRole == ToolRole {
			boundary--
			for boundary > 1 && history[boundary-1].Role == ToolRole {
				boundary--
			}
			if boundary > 1 && history[boundary-1].Role == Assistant {
				boundary--
			}
			continue
		}

		if prevRole == Assistant && len(history[prevIdx].ToolCalls) > 0 {
			boundary--
			continue
		}

		break
	}
	return boundary
}

// checkSilentOverflow detects providers that silently accept an oversized
// prompt and return a successful response instead of an error (e.g. z.ai,
// Xiaomi MiMo-style truncation).  When the estimated context usage exceeds
// the configured window, it schedules compression for the next turn.
func (a *Agent) checkSilentOverflow() {
	maxTokens := a.effectiveMaxTokens()
	if maxTokens == 0 {
		return
	}
	stats := a.computeContextStats()
	if stats.UsagePercent < 95 {
		return
	}
	a.cfg.Logger.Log(Warn, "Silent overflow detected: %d%% usage (%d / %d tokens)",
		stats.UsagePercent, stats.EstimatedTokens, stats.MaxTokens)
	a.emitEvent(OutputEvent{
		Type:         EventContextStats,
		ContextStats: &stats,
		Text:         "warning: context usage ≥ 95% without provider error — proactive compression will fire on next turn",
	})
}

// handleContextError checks if the error is a context-length error and, if
// OnContextError is enabled, applies the configured compression strategy
// to free context space.  Unlike compressToolElision (which only elides
// tool calls/results), this uses the full configured strategy — including
// selective (message removal) and summarization — so text-heavy
// conversations are handled too.
//
// When the configured strategy is tool_elision or micro (which may leave
// text content untouched), it escalates to selective as a fallback so the
// retry can make progress.
func (a *Agent) handleContextError(err error) {
	if !a.cfg.ContextCompression.OnContextError {
		return
	}
	if !isContextLengthError(err) {
		return
	}

	strategy := CompressionStrategy(a.cfg.ContextCompression.Strategy)
	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "Context length error — applying compression (strategy=%s)", strategy)
	}

	// Apply the configured strategy.  We pass force=true so the strategy
	// runs even when internal thresholds aren't met (overflow is an emergency).
	a.compressHistoryWithStrategy(string(strategy), true)

	// If the configured strategy is tool_elision or micro and context is
	// STILL over the hard ceiling, escalate to selective (remove old turns).
	// This handles text-heavy conversations where eliding tool data leaves
	// all user+assistant messages intact.
	if strategy == "" || strategy == CompressionToolElision || strategy == CompressionMicro {
		stats := a.computeContextStats()
		maxTokens := a.effectiveMaxTokens()
		if maxTokens > 0 && stats.EstimatedTokens > maxTokens*90/100 {
			if a.cfg.Logger != nil {
				a.cfg.Logger.Log(Info, "Tool elision/micro freed insufficient space (%d/%d tokens) — escalating to selective",
					stats.EstimatedTokens, maxTokens)
			}
			a.compressSelective()
		}
	}
}

// compressHistoryWithStrategy applies the named compression strategy
// directly (empty = tool_elision).  The force parameter bypasses internal
// per-strategy thresholds.
func (a *Agent) compressHistoryWithStrategy(strategy string, force bool) {
	// Build a temporary Ctx-free strategy dispatch.  The summarization
	// strategy needs a real context, so we skip it here (it is not a
	// useful emergency strategy anyway since it costs an LLM call).
	switch CompressionStrategy(strategy) {
	case CompressionSelective:
		a.compressSelective()
	case CompressionToolElision:
		a.compressToolElision(force)
	case CompressionMicro:
		a.microCompactForced(force)
	case CompressionHybrid:
		a.compressToolElision(true)
		stats := a.computeContextStats()
		maxTokens := a.effectiveMaxTokens()
		if maxTokens > 0 && stats.EstimatedTokens > maxTokens*90/100 {
			a.compressSelective()
		}
	default:
		a.compressToolElision(force)
	}
}

// isContextLengthError reports whether the error indicates the LLM's context
// window was exceeded. It uses both structured classification (checking
// ProviderError.IsContextOverflow via hooks.IsContextOverflow) and string
// matching so that all provider error formats are recognised.
func isContextLengthError(err error) bool {
	if err == nil {
		return false
	}
	// Structured check first — catches ProviderError where the hook
	// pipeline already classified IsContextOverflow=true.
	if hooks.IsContextOverflow(err) {
		return true
	}
	// Fallback string matching for errors not wrapped as ProviderError.
	msg := strings.ToLower(err.Error())
	patterns := []string{
		"context_length_exceeded",
		"context length",
		"maximum context",
		"token limit",
		"max_tokens",
		"too many tokens",
		"context window",
	}
	for _, p := range patterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// SetBufferedToolCallCountForTest manually sets the buffered tool call
// counter. It is intended only for tests that exercise status labels without
// driving a real stream.
func (a *Agent) SetBufferedToolCallCountForTest(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.bufferedToolCallCount = n
}
