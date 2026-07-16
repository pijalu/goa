// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"context"
	"fmt"

	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"
	"time"

	"github.com/pijalu/goa/internal/agentic"
	gorole "github.com/pijalu/goa/internal/role"
	"github.com/pijalu/goa/prompts"
)

// workflowRunContext bundles the cancellable context for an in-flight
// workflow and its cleanup function.
type workflowRunContext struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// WorkflowMode determines who drives the multi-agent workflow.
//
//	Agent-Driven (Tool-Driven):
//	  The main LLM agent self-initiates workflows by calling special tools
//	  (request_review, delegate_to). The agent decides when it needs help.
//	  The user enables this with /agent-driven:on.
//	  Example: the agent finishes writing code and calls "request_review".
//
//	Framework-Driven (Explicit):
//	  The framework (not the LLM) triggers workflows via slash commands.
//	  The user has full control over when workflows run.
//	  Examples:
//	    • /companion:on  → companion agent after every main turn (CompanionMinor)
//	    • /workflows:run:review  → one-shot review workflow
//	    • /workflows:run:pair    → planner → coder workflow
//	    • /agent-driven:on       → agent calls tools (AgentDriven)
type WorkflowMode int

const (
	WorkflowInactive       WorkflowMode = iota
	WorkflowCompanionMinor              // companion after each main turn
	WorkflowAgentDriven                 // agent calls request_review/delegate_to
	WorkflowRunning                     // workflow execution — suspends companion mode
)

// OutputHandler is called when a sub-agent produces output text.
type OutputHandler func(agentName string, text string)

// GateDecision represents a user decision at an approval gate.
type GateDecision struct {
	Action string // "approve", "skip", "retry"
}

// WorkflowProgress tracks the current execution state of a workflow.
type WorkflowProgress struct {
	StageIndex  int
	TotalStages int
	StageName   string
	StageID     string
	Status      string // "running", "gate", "complete", "failed"
}

// SteeringQueue is the interface used by ForegroundOrchestrator for buffered
// user steering input. It matches core.SteeringQueue so the same queue can be
// shared between the agent and orchestration paths.
type SteeringQueue interface {
	Append(string)
	Flush() []string
	Len() int
}

// ForegroundOrchestrator runs agents synchronously in the main goroutine.
// User sees each step as it happens via TUI events, and can steer mid-flight.
type ForegroundOrchestrator struct {
	mu sync.RWMutex

	mainAgent *agentic.Agent
	subAgents map[string]*agentic.Agent // role name → agent
	pool      *AgentPool
	mode      WorkflowMode
	promptReg *prompts.Registry

	// Steering
	steerQueue SteeringQueue

	// TUI integration
	events chan OrchestratorMessage

	// Sub-agent output tracking (S4)
	lastOutputs map[string]string // agent name → last text output
	outputMu    sync.Mutex
	outputHndlr OutputHandler // called when sub-agents produce text output

	// Gate approval
	gateCh chan GateDecision

	// Workflow progress
	progress   WorkflowProgress
	progressMu sync.Mutex

	// Active workflow tracking
	activePipeline *Pipeline
	activeRun      *PipelineRun

	// accumulatedContext stores the output from completed stages,
	// passed as context to the next stage's prompt.
	accumulatedContext string

	// stageToolCount tracks how many tools the current stage agent has called.
	// Used by WorkflowNextTool to validate work was done before advancing.
	stageToolCount atomic.Int32

	// stageAdvanced is the sentinel that distinguishes a WorkflowNextTool-driven
	// stage advance from a user/parent Cancel(). WorkflowNextTool sets it before
	// cancelling the stage context; runStage treats a plain context.Canceled
	// (sentinel unset) as a HARD abort returning ctx.Err() (BUG-05).
	stageAdvanced atomic.Bool

	// stageContext is used to cancel the current stage's agent run.
	// When WorkflowNextTool.Execute() is called, it cancels this context
	// so runStage returns and the orchestrator advances to the next stage.
	stageCancel context.CancelFunc

	// ModeSwitchCallback is called before each stage to switch the agent mode.
	// The callback receives the stage agent name (planner, coder, reviewer).
	ModeSwitchCallback func(agentName string)

	// savedMode stores the WorkflowMode before workflow execution
	// so ResumeCompanion can restore it.
	savedMode WorkflowMode

	// Lifecycle
	stopped bool
	stopMu  sync.Mutex

	// Communication bus for agent-to-agent messaging.
	agentBus *agentic.AgentBus

	// Framework-driven companion back-and-forth cycle counter.
	companionMsgCount int
	companionMsgMax   int
	companionCountMu  sync.Mutex

	// runCtx is the cancellable context for the currently running workflow.
	runCtx *workflowRunContext
	runMu  sync.Mutex
}

// NewForegroundOrchestrator creates a foreground orchestrator.
// It wires the pool's OnAgentCreated hook so every sub-agent forwards its
// output text to the orchestrator's OutputHandler (if set).
// defaultSteeringQueue is a minimal in-memory steering queue used when no
// external queue is injected. It keeps the orchestrator self-contained and
// matches the cap-1 channel semantics for single-steering use cases.
type defaultSteeringQueue struct {
	mu      sync.Mutex
	pending []string
}

func (q *defaultSteeringQueue) Append(text string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = append(q.pending, text)
}

func (q *defaultSteeringQueue) Flush() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	pending := q.pending
	q.pending = nil
	return pending
}

func (q *defaultSteeringQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

func NewForegroundOrchestrator(pool *AgentPool) *ForegroundOrchestrator {
	o := &ForegroundOrchestrator{
		subAgents:   make(map[string]*agentic.Agent),
		pool:        pool,
		steerQueue:  &defaultSteeringQueue{},
		gateCh:      make(chan GateDecision, 1),
		events:      make(chan OrchestratorMessage, 1000),
		lastOutputs: make(map[string]string),
	}

	// Wire OnAgentCreated so every newly created sub-agent automatically
	// forwards its assistant output to the TUI via the orchestrator's events
	// channel. System prompt and user messages are filtered out — only
	// assistant responses are accumulated and forwarded on turn end.
	pool.OnAgentCreated = o.makeAgentCreatedHook()

	return o
}

// Context returns a context derived from the orchestrator's root context.
// Commands use this when invoking workflow/pair/review runs so the user can
// cancel the work mid-flight via Cancel().
func (o *ForegroundOrchestrator) Context() context.Context {
	o.runMu.Lock()
	defer o.runMu.Unlock()
	if o.runCtx != nil {
		return o.runCtx.ctx
	}
	return context.Background()
}

// startRunContext creates a new cancellable context for a workflow run and
// stores it so Cancel() can abort it.
func (o *ForegroundOrchestrator) startRunContext(parent context.Context) context.Context {
	o.runMu.Lock()
	defer o.runMu.Unlock()
	if o.runCtx != nil {
		o.runCtx.cancel()
	}
	ctx, cancel := context.WithCancel(parent)
	o.runCtx = &workflowRunContext{ctx: ctx, cancel: cancel}
	return ctx
}

// finishRunContext clears the stored run context.
func (o *ForegroundOrchestrator) finishRunContext() {
	o.runMu.Lock()
	defer o.runMu.Unlock()
	o.runCtx = nil
}

// Cancel aborts any in-flight workflow run. It is safe to call from any
// goroutine (e.g., a /workflows:cancel command or an Esc key handler).
func (o *ForegroundOrchestrator) Cancel() {
	o.runMu.Lock()
	rc := o.runCtx
	o.runMu.Unlock()
	if rc != nil {
		rc.cancel()
	}
}

func (o *ForegroundOrchestrator) makeAgentCreatedHook() func(string, *agentic.Agent) {
	return func(role string, agent *agentic.Agent) {
		state := &agentOutputState{}
		agent.AddObserver(agentic.OutputObserverFunc(func(ev agentic.OutputEvent) {
			handleAgentOutputEvent(o, role, state, ev)
		}))
	}
}

type agentOutputState struct {
	agentBuf       string
	thinkingBuf    string
	streamActive   bool
	thinkingActive bool
}

func handleAgentOutputEvent(o *ForegroundOrchestrator, role string, state *agentOutputState, ev agentic.OutputEvent) {
	switch ev.Type {
	case agentic.EventToolCall:
		// Track tool calls during this stage. WorkflowNextTool uses this
		// to validate that actual work was done before allowing advancement.
		if ev.ToolName != "workflows_next" {
			o.stageToolCount.Add(1)
		}
	case agentic.EventContent:
		handleAgentContentEvent(o, role, state, ev)
	case agentic.EventEnd:
		handleAgentEndEvent(o, role, state)
	}
}

func handleAgentContentEvent(o *ForegroundOrchestrator, role string, state *agentOutputState, ev agentic.OutputEvent) {
	if ev.Text == "" || ev.Role != agentic.Assistant {
		return
	}
	if ev.State == agentic.StateThinking {
		if !state.thinkingActive {
			state.thinkingActive = true
			state.thinkingBuf = ""
			o.emitKind(role, "stream_start", "", "thinking_start")
		}
		state.thinkingBuf += ev.Text
		o.emitKind(role, "stream_chunk", ev.Text, "thinking_chunk")
		return
	}
	if state.thinkingActive {
		state.finishThinking(o, role)
	}
	if !state.streamActive {
		state.streamActive = true
		o.emitKind(role, "stream_start", "", "content")
	}
	o.emitKind(role, "stream_chunk", ev.Text, "content")
	state.agentBuf += ev.Text
}

func handleAgentEndEvent(o *ForegroundOrchestrator, role string, state *agentOutputState) {
	if state.thinkingActive {
		state.finishThinking(o, role)
	}
	full := state.agentBuf
	state.agentBuf = ""
	state.streamActive = false

	o.RecordOutput(role, full)
	o.emitKind(role, "stream_end", full, "content")
}

func (s *agentOutputState) finishThinking(o *ForegroundOrchestrator, role string) {
	s.thinkingActive = false
	o.emitKind(role, "thinking_end", s.thinkingBuf, "thinking_end")
	s.thinkingBuf = ""
}

// SetPromptRegistry sets the prompt registry for loading system/user prompts.
func (o *ForegroundOrchestrator) SetPromptRegistry(reg *prompts.Registry) {
	o.promptReg = reg
}

// SetMainAgent sets the main (user-facing) agent.
func (o *ForegroundOrchestrator) SetMainAgent(a *agentic.Agent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.mainAgent = a
}

// SetAgentBus sets the communication bus used for agent-to-agent messaging.
func (o *ForegroundOrchestrator) SetAgentBus(bus *agentic.AgentBus) {
	o.agentBus = bus
}

// SetCompanionMaxMessages sets the maximum number of framework-driven
// companion messages before the loop is forced to end. A value <= 0 uses
// the default (10).
func (o *ForegroundOrchestrator) SetCompanionMaxMessages(n int) {
	o.companionCountMu.Lock()
	defer o.companionCountMu.Unlock()
	if n <= 0 {
		n = defaultCompanionMaxMessages
	}
	o.companionMsgMax = n
}

// ResetCompanionCount resets the framework-driven companion message counter.
// Called when the main agent performs a tool call.
func (o *ForegroundOrchestrator) ResetCompanionCount() {
	o.companionCountMu.Lock()
	defer o.companionCountMu.Unlock()
	o.companionMsgCount = 0
}

// CompanionCount returns the current and maximum framework-driven companion
// message counts.
func (o *ForegroundOrchestrator) CompanionCount() (int, int) {
	o.companionCountMu.Lock()
	defer o.companionCountMu.Unlock()
	if o.companionMsgMax <= 0 {
		return o.companionMsgCount, defaultCompanionMaxMessages
	}
	return o.companionMsgCount, o.companionMsgMax
}

const defaultCompanionMaxMessages = 10

// SetMode sets the workflow mode.
func (o *ForegroundOrchestrator) SetMode(mode WorkflowMode) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.mode = mode
}

// Mode returns the current workflow mode.
func (o *ForegroundOrchestrator) Mode() WorkflowMode {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.mode
}

// ModeLabel returns a human-readable label for the current workflow mode.
// Empty string means no workflow mode is active.
func (o *ForegroundOrchestrator) ModeLabel() string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	switch o.mode {
	case WorkflowCompanionMinor:
		return "companion"
	case WorkflowAgentDriven:
		return "agent-driven"
	default:
		return ""
	}
}

// Events returns the channel for orchestrator messages (sent to TUI).
func (o *ForegroundOrchestrator) Events() <-chan OrchestratorMessage {
	return o.events
}

// SetOutputHandler sets a callback that is called for every text output
// produced by sub-agents. Wire this to the TUI event bus in main.go.
func (o *ForegroundOrchestrator) SetOutputHandler(h OutputHandler) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.outputHndlr = h
}

func (o *ForegroundOrchestrator) setStageCancel(cancel context.CancelFunc) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stageCancel = cancel
}

func (o *ForegroundOrchestrator) clearStageCancel() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stageCancel = nil
}

// cancelStage cancels the currently running stage, if any. It is safe to call
// from any goroutine (e.g. a tool execution handler).
func (o *ForegroundOrchestrator) cancelStage() {
	o.mu.Lock()
	cancel := o.stageCancel
	o.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// markStageAdvanced is called by WorkflowNextTool to signal that the current
// stage's work is complete and the workflow should advance. It sets the
// stageAdvanced sentinel BEFORE cancelling the stage context so that runStage
// can distinguish this advance from a user/parent Cancel() (BUG-05).
func (o *ForegroundOrchestrator) markStageAdvanced() {
	o.stageAdvanced.Store(true)
	o.cancelStage()
}

// resetStageState clears the transient per-stage state (stageCancel field,
// stageToolCount, stageAdvanced) at a stage boundary. Clearing stageToolCount
// together with stageCancel ensures a WorkflowNextTool that races during a
// gate window (handleStageGate blocks up to 30 minutes) cannot observe a
// stale tool count from the just-finished stage (BUG-12).
func (o *ForegroundOrchestrator) resetStageState() {
	o.clearStageCancel()
	o.stageToolCount.Store(0)
	o.stageAdvanced.Store(false)
}

// SetSteeringQueue sets the buffered steering queue. When non-nil, InjectSteering
// appends to it and checkSteering flushes from it, allowing the same queue to be
// shared with the main agent steering path.
func (o *ForegroundOrchestrator) SetSteeringQueue(sq SteeringQueue) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.steerQueue = sq
}

// InjectSteering injects a user steering message into the next cycle.
func (o *ForegroundOrchestrator) InjectSteering(text string) {
	o.mu.Lock()
	sq := o.steerQueue
	o.mu.Unlock()
	if sq != nil {
		sq.Append(text)
		return
	}
}

// AfterMainTurn runs after the main agent completes a turn.
// In companion-minor mode, it formats the main output and sends it to
// the companion agent for review. The companion's behavior is determined
// by its system prompt (e.g. prompts/companion.md).
func (o *ForegroundOrchestrator) AfterMainTurn(ctx context.Context, mainOutput string) error {
	// Don't run companion during workflow execution — the workflow
	// defines its own collaboration model between stage agents.
	o.mu.RLock()
	mode := o.mode
	run := o.activeRun
	o.mu.RUnlock()
	if mode != WorkflowCompanionMinor || run != nil {
		return nil
	}

	// Check budget BEFORE running the companion to avoid wasted LLM calls.
	if o.companionCountExceeded() {
		o.emit("system", "main", "Companion: max cycles reached")
		return nil
	}

	o.emit("system", gorole.Companion, "Companion called")

	companion, err := o.pool.GetOrCreate(gorole.Companion)
	if err != nil {
		return fmt.Errorf("create companion: %w", err)
	}

	prompt := o.renderCompanionPrompt(mainOutput)
	if err := companion.Run(ctx, prompt); err != nil {
		return fmt.Errorf("companion run: %w", err)
	}

	review := o.CollectLastMessage(gorole.Companion)
	if review == "" {
		return nil
	}

	if strings.Contains(review, DefaultCompanionEndDelimiter) {
		return nil
	}

	return o.sendCompanionMessageToMain(review)
}

func (o *ForegroundOrchestrator) renderCompanionPrompt(mainOutput string) string {
	if o.promptReg == nil {
		return mainOutput
	}
	rendered, err := o.promptReg.Render("companion_review", map[string]string{
		"MainOutput":   mainOutput,
		"EndDelimiter": DefaultCompanionEndDelimiter,
	})
	if err != nil {
		return mainOutput
	}
	return rendered
}

func (o *ForegroundOrchestrator) sendCompanionMessageToMain(review string) error {
	o.companionCountMu.Lock()
	o.companionMsgCount++
	o.companionCountMu.Unlock()

	o.emitKind("system", "main", "", "companion_cycle")

	if o.agentBus == nil {
		return fmt.Errorf("no agent bus configured")
	}

	labeled := fmt.Sprintf("Message from companion:\n```\n%s\n```", review)
	return o.agentBus.Send(context.Background(), agentic.CommMessage{
		From:    gorole.Companion,
		To:      gorole.Main,
		Content: labeled,
	})
}

// RunWorkflow executes a registered workflow by ID.
func (o *ForegroundOrchestrator) RunWorkflow(ctx context.Context, reg *WorkflowRegistry, workflowID, userInput string) error {
	wf, ok := reg.Get(workflowID)
	if !ok {
		return fmt.Errorf("workflow %q not found", workflowID)
	}

	ctx = o.startRunContext(ctx)
	defer o.finishRunContext()

	run := NewPipelineRun(&wf)
	o.mu.Lock()
	o.activePipeline = &wf
	o.activeRun = run
	o.accumulatedContext = ""
	o.mu.Unlock()

	o.setProgress(WorkflowProgress{
		StageIndex:  0,
		TotalStages: len(wf.Stages),
		StageName:   "starting",
		StageID:     "",
		Status:      "running",
	})

	for i, stage := range wf.Stages {
		if o.Stopped() {
			o.setProgress(WorkflowProgress{Status: "failed"})
			return fmt.Errorf("orchestrator stopped")
		}

		// Start the stage via NextStage
		_, ok := run.NextStage()
		if !ok {
			break
		}

		o.setProgress(WorkflowProgress{
			StageIndex:  i,
			TotalStages: len(wf.Stages),
			StageName:   stage.Name,
			StageID:     stage.ID,
			Status:      "running",
		})
		// Switch to the correct mode for this stage
		if o.ModeSwitchCallback != nil {
			o.ModeSwitchCallback(stage.Agent)
		}

		if err := o.runStage(ctx, run, stage, userInput); err != nil {
			o.setProgress(WorkflowProgress{Status: "failed"})
			return err
		}
	}

	o.setProgress(WorkflowProgress{Status: "complete"})
	o.emit("system", "user", fmt.Sprintf("Workflow %q complete.", workflowID))

	// Clear active run so companion mode (if enabled) works after workflow.
	o.mu.Lock()
	o.activeRun = nil
	o.activePipeline = nil
	o.accumulatedContext = ""
	o.mu.Unlock()

	return nil
}

func (o *ForegroundOrchestrator) runStage(ctx context.Context, run *PipelineRun, stage PipelineStage, userInput string) error {
	o.emit("system", stage.Agent, fmt.Sprintf("Running %s...", stage.Name))
	// Fresh stage: clear leftover transient state so counts or a stale advance
	// sentinel from a previous stage cannot leak into this one (BUG-12).
	o.stageToolCount.Store(0)
	o.stageAdvanced.Store(false)

	agent, err := o.pool.GetOrCreate(stage.Agent)
	if err != nil {
		return fmt.Errorf("create agent %q: %w", stage.Agent, err)
	}

	// Add accumulated context from previous stages
	o.mu.RLock()
	acc := o.accumulatedContext
	o.mu.RUnlock()
	stageInput := userInput
	if acc != "" {
		stageInput = "Previous stage output:\n" + acc + "\n\n" + userInput
	}

	prompt, err := o.resolveStagePrompt(stage, stageInput)
	if err != nil {
		return fmt.Errorf("resolve prompt for stage %q: %w", stage.ID, err)
	}

	// Use a cancellable context so WorkflowNextTool can advance the stage.
	stageCtx, stageCancel := context.WithCancel(ctx)
	o.setStageCancel(stageCancel)
	// Always release the stage ctx subtree to avoid leaking it (the cancel func
	// is also stored on the orchestrator, so go vet -lostcancel cannot see it).
	defer stageCancel()

	if err := agent.Run(stageCtx, prompt); err != nil {
		return o.handleStageRunError(ctx, run, stage, err)
	}

	// Stage agent run finished normally. Reset per-stage state NOW (before the
	// gate) so a WorkflowNextTool racing in the gate window observes no stale
	// tool count or dangling cancel (BUG-12).
	o.resetStageState()
	run.CompleteStage(stage.ID)

	// Collect the last assistant message as context for the next stage
	if output := o.CollectLastMessage(stage.Agent); output != "" {
		o.mu.Lock()
		if o.accumulatedContext != "" {
			o.accumulatedContext += "\n\n"
		}
		o.accumulatedContext += fmt.Sprintf("[%s] %s", stage.Name, output)
		o.mu.Unlock()
	}

	o.handleStageGate(stage)

	if text, ok := o.checkSteering(); ok {
		o.emit("system", stage.Agent, "Steering: "+text)
	}
	return nil
}

// handleStageRunError classifies an error returned by agent.Run during a stage:
//   - A WorkflowNextTool-driven advance (stageAdvanced sentinel set) completes
//     the stage and returns nil so RunWorkflow proceeds to the next stage.
//   - A plain context.Canceled originates from the run context (user Cancel()
//     or parent cancellation) and is a HARD abort (BUG-05): return ctx.Err().
//   - Any other error is surfaced and returned.
func (o *ForegroundOrchestrator) handleStageRunError(ctx context.Context, run *PipelineRun, stage PipelineStage, err error) error {
	if errors.Is(err, context.Canceled) && o.stageAdvanced.Load() {
		// WorkflowNextTool-driven advance.
		o.resetStageState()
		run.CompleteStage(stage.ID)
		return nil
	}
	o.emit("system", stage.Agent, fmt.Sprintf("%s error: %v", stage.Name, err))
	o.resetStageState()
	if errors.Is(err, context.Canceled) {
		// User/parent cancellation — hard abort, do not advance.
		return ctx.Err()
	}
	return err
}

func (o *ForegroundOrchestrator) resolveStagePrompt(stage PipelineStage, userInput string) (string, error) {
	// Use the pipeline's directory for relative prompt resolution
	wfDir := ""
	if o.activePipeline != nil {
		wfDir = o.activePipeline.Dir
	}
	prompt, err := ResolvePromptWithDir(stage, o.promptReg, wfDir)
	if err != nil {
		return "", err
	}

	// Render the prompt as a Go template with UserInput variable.
	// Use the resolved prompt text directly, not the stage.Prompt name.
	if strings.Contains(prompt, "{{.") || strings.Contains(userInput, "{{") {
		tmpl, err := template.New("stage").Parse(prompt)
		if err == nil {
			var buf strings.Builder
			if err := tmpl.Execute(&buf, map[string]string{
				"UserInput": userInput,
			}); err == nil {
				prompt = buf.String()
			}
		}
	}
	return prompt, nil
}

func (o *ForegroundOrchestrator) handleStageGate(stage PipelineStage) {
	if !stage.Gate.RequireApproval {
		return
	}
	gatePrompt := stage.Gate.Prompt
	if gatePrompt == "" {
		gatePrompt = fmt.Sprintf("Approve %s?", stage.Name)
	}
	o.setProgress(WorkflowProgress{
		StageName: stage.Name,
		StageID:   stage.ID,
		Status:    "gate",
	})
	o.emitGateApproval(stage.ID, stage.Name, gatePrompt)
	decision := o.waitForGateApproval()
	switch decision {
	case "approve":
		o.emit("system", stage.Agent, "Gate approved.")
	case "skip":
		o.emit("system", stage.Agent, "Gate skipped.")
	case "retry":
		o.emit("system", stage.Agent, "Gate retry requested.")
	}
}

func (o *ForegroundOrchestrator) emitGateApproval(stageID, stageName, prompt string) {
	// The TUI layer will detect the special gate message format and show a selector
	o.emit("gate", "user", fmt.Sprintf("GATE_APPROVAL:%s|%s|%s", stageID, stageName, prompt))
}

// RunPair runs the pair workflow: planner → coder.
func (o *ForegroundOrchestrator) RunPair(ctx context.Context, userInput string) error {
	ctx = o.startRunContext(ctx)
	defer o.finishRunContext()

	o.emit("system", gorole.Planner, "Planning task: "+userInput)

	planner, err := o.pool.GetOrCreate(gorole.Planner)
	if err != nil {
		return fmt.Errorf("create planner: %w", err)
	}
	coder, err := o.pool.GetOrCreate(gorole.Coder)
	if err != nil {
		return fmt.Errorf("create coder: %w", err)
	}

	planPrompt := fmt.Sprintf("Decompose this task into implementable steps:\n%s", userInput)
	if o.promptReg != nil {
		rendered, err := o.promptReg.Render("pair.planner", map[string]string{"UserInput": userInput})
		if err == nil {
			planPrompt = rendered
		}
	}

	o.emit("system", gorole.Planner, "Running planner...")
	if err := planner.Run(ctx, planPrompt); err != nil {
		o.emit("system", gorole.Planner, fmt.Sprintf("Planner error: %v", err))
		return err
	}

	if text, ok := o.checkSteering(); ok {
		o.emit(gorole.Planner, gorole.Coder, "Steering: "+text)
	} else {
		o.emit(gorole.Planner, gorole.Coder, "Plan complete, handing off to coder.")
	}

	codePrompt := fmt.Sprintf("Implement based on the plan.\nUser request: %s\nPlan has been created.", userInput)
	if o.promptReg != nil {
		rendered, err := o.promptReg.Render("pair.coder", map[string]string{"UserInput": userInput})
		if err == nil {
			codePrompt = rendered
		}
	}

	o.emit("system", gorole.Coder, "Running coder...")
	if err := coder.Run(ctx, codePrompt); err != nil {
		o.emit("system", gorole.Coder, fmt.Sprintf("Coder error: %v", err))
		return err
	}

	o.emit(gorole.Coder, "user", "Implementation complete.")
	return nil
}

// RunReview runs the review workflow: content → companion feedback.
func (o *ForegroundOrchestrator) RunReview(ctx context.Context, content string) error {
	ctx = o.startRunContext(ctx)
	defer o.finishRunContext()

	o.emit("system", gorole.Companion, "Reviewing content...")

	companion, err := o.pool.GetOrCreate(gorole.Companion)
	if err != nil {
		return fmt.Errorf("create companion: %w", err)
	}

	prompt := content
	if o.promptReg != nil {
		rendered, err := o.promptReg.Render("pipeline.review", nil)
		if err == nil {
			prompt = rendered + "\n\n" + content
		}
	}

	if err := companion.Run(ctx, prompt); err != nil {
		o.emit("system", gorole.Companion, fmt.Sprintf("Companion error: %v", err))
		return err
	}

	o.emit(gorole.Companion, "user", "Review complete.")
	return nil
}

// Stop stops the orchestrator.
func (o *ForegroundOrchestrator) Stop() {
	o.stopMu.Lock()
	defer o.stopMu.Unlock()
	o.stopped = true
	// Unblock any waiting gate
	select {
	case o.gateCh <- GateDecision{Action: "skip"}:
	default:
	}
}

// Stopped returns true if the orchestrator has been stopped.
func (o *ForegroundOrchestrator) Stopped() bool {
	o.stopMu.Lock()
	defer o.stopMu.Unlock()
	return o.stopped
}

// SubmitGateDecision submits a user decision for the current gate.
func (o *ForegroundOrchestrator) SubmitGateDecision(decision GateDecision) {
	select {
	case o.gateCh <- decision:
	default:
	}
}

// Progress returns the current workflow progress.
// ActiveRun returns the current pipeline run, or nil if no workflow is active.
func (o *ForegroundOrchestrator) ActiveRun() *PipelineRun {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.activeRun
}

// ActivePipeline returns the current workflow pipeline, or nil.
func (o *ForegroundOrchestrator) ActivePipeline() *Pipeline {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.activePipeline
}

// AccumulatedContext returns the context accumulated from completed stages.
func (o *ForegroundOrchestrator) AccumulatedContext() string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.accumulatedContext
}

// StageToolCount returns how many non-workflows_next tools the current
// stage agent has called. Used by WorkflowNextTool to validate work.
func (o *ForegroundOrchestrator) StageToolCount() int {
	return int(o.stageToolCount.Load())
}

// SetStageToolCount sets the tool call count (used for testing).
func (o *ForegroundOrchestrator) SetStageToolCount(n int) {
	o.stageToolCount.Store(int32(n))
}

// SuspendCompanion saves and disables companion mode during workflow execution.
// Companion mode triggers AfterMainTurn which would interfere with the
// workflow's own agent orchestration.
func (o *ForegroundOrchestrator) SuspendCompanion() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.mode == WorkflowCompanionMinor {
		o.savedMode = o.mode
		o.mode = WorkflowInactive
	}
}

// ResumeCompanion restores the companion mode saved by SuspendCompanion.
func (o *ForegroundOrchestrator) ResumeCompanion() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.savedMode == WorkflowCompanionMinor {
		o.mode = WorkflowCompanionMinor
		o.savedMode = WorkflowInactive
	}
}

func (o *ForegroundOrchestrator) Progress() WorkflowProgress {
	o.progressMu.Lock()
	defer o.progressMu.Unlock()
	return o.progress
}

func (o *ForegroundOrchestrator) setProgress(p WorkflowProgress) {
	o.progressMu.Lock()
	defer o.progressMu.Unlock()
	o.progress = p
}

// waitForGateApproval blocks until the user submits a gate decision or the
// orchestrator is stopped. Returns the decision action string.
func (o *ForegroundOrchestrator) waitForGateApproval() string {
	select {
	case decision := <-o.gateCh:
		return decision.Action
	case <-time.After(30 * time.Minute):
		return "skip" // timeout: auto-skip
	}
}

// checkSteering checks if user steering messages are available, non-blocking.
func (o *ForegroundOrchestrator) checkSteering() (string, bool) {
	o.mu.Lock()
	sq := o.steerQueue
	o.mu.Unlock()
	if sq == nil {
		return "", false
	}
	pending := sq.Flush()
	if len(pending) == 0 {
		return "", false
	}
	return strings.Join(pending, "\n\n"), true
}

func (o *ForegroundOrchestrator) companionCountExceeded() bool {
	o.companionCountMu.Lock()
	defer o.companionCountMu.Unlock()
	max := o.companionMsgMax
	if max <= 0 {
		max = defaultCompanionMaxMessages
	}
	return o.companionMsgCount >= max
}

// companion review round. The companion system prompt and review prompt
// are templated with this value so the framework can detect it.
const DefaultCompanionEndDelimiter = "</end>"

// emit sends an orchestrator message to the events channel.
func (o *ForegroundOrchestrator) emit(from, to, content string) {
	o.emitKind(from, to, content, "content")
}

func (o *ForegroundOrchestrator) emitKind(from, to, content, kind string) {
	msg := OrchestratorMessage{
		From: from, To: to, Content: content, Kind: kind,
		Timestamp: time.Now(),
	}
	// High-frequency deltas (To == "stream_chunk", both thinking and content
	// variants) are lossy: drop rather than block the agent loop. They are
	// best-effort — the consumer reconciles the final text at stream_end, so a
	// dropped chunk only costs live-update smoothness, never correctness.
	if to == "stream_chunk" {
		select {
		case o.events <- msg:
		default:
			// Intentionally silent: chunks are droppable by design.
		}
		return
	}
	// Structural events (To == stream_start / stream_end / thinking_end, every
	// lifecycle message, and GATE_APPROVAL) must NOT be silently dropped: a
	// lost stream_end breaks the UI state machine and a lost gate message hangs
	// the workflow. Apply backpressure (block until the consumer makes room)
	// instead — the same "never drop EventEnd" principle used for the agent bus.
	o.events <- msg
}
