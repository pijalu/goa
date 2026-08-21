// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/multiagent"
)

// headlessExitCode names the exit codes used by runHeadless.
const (
	headlessExitOK            = 0
	headlessExitConfigError   = 1
	headlessExitProviderError = 2
	headlessExitMaxTurns      = 3
	headlessExitTimeout       = 4
	headlessExitGoalFailed    = 5
	headlessExitOrchFailed    = 6
)

// HeadlessRenderer renders agent output directly to a writer without TUI
// buffering or screen-oriented updates.
type HeadlessRenderer interface {
	UserPrompt(prompt string)
	AssistantChunk(text string)
	ThinkingStart()
	ThinkingChunk(text string)
	ThinkingEnd()
	ToolCall(name, id, input string)
	ToolResult(name, id, output string)
	Stats(stats sessionStats, turn int)
	Summary(stats sessionStats, turns int, totalTime time.Duration)
	Error(msg string)
	AssistantStreamEnd()
	CompanionStart(cycle int)
	CompanionEnd(cycle int)
	CompanionThinkingStart()
	CompanionThinkingChunk(text string)
	CompanionThinkingEnd()
	CompanionChunk(text string)
	Flush()
}

// ConfirmStrategy decides how to answer tool confirmation requests in
// headless mode.
type ConfirmStrategy interface {
	// Confirm returns true if the tool call should be executed.
	Confirm(toolName, input string) (bool, error)
}

// autoConfirmStrategy always approves.
type autoConfirmStrategy struct{}

func (autoConfirmStrategy) Confirm(_, _ string) (bool, error) { return true, nil }

// ttyConfirmStrategy prompts on the given writer and reads from the reader.
type ttyConfirmStrategy struct {
	in  *bufio.Reader
	out io.Writer
}

func (s *ttyConfirmStrategy) Confirm(toolName, input string) (bool, error) {
	desc := toolConfirmDescription(toolName, input)
	fmt.Fprintf(s.out, "Approve %s [y/N]? ", desc)
	line, err := s.in.ReadString('\n')
	if err != nil {
		return false, err
	}
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes", nil
}

// rejectConfirmStrategy always rejects and is used when confirmation is
// required but no TTY is available.
type rejectConfirmStrategy struct {
	out io.Writer
}

func (s *rejectConfirmStrategy) Confirm(toolName, input string) (bool, error) {
	desc := toolConfirmDescription(toolName, input)
	fmt.Fprintf(s.out, "Rejected %s (non-interactive headless mode)\n", desc)
	return false, nil
}

func toolConfirmDescription(toolName, input string) string {
	// Keep input to a single line for the prompt.
	oneLine := strings.Join(strings.Fields(input), " ")
	if len(oneLine) > 80 {
		oneLine = oneLine[:77] + "..."
	}
	if oneLine == "" {
		return toolName
	}
	return fmt.Sprintf("%s: %s", toolName, oneLine)
}

// headlessStreamKind tracks the active streaming block.
type headlessStreamKind int

const (
	headlessStreamNone headlessStreamKind = iota
	headlessStreamThinking
	headlessStreamAssistant
)

// headlessStreamState tracks the current streaming context.
type headlessStreamState struct {
	kind headlessStreamKind
	text strings.Builder
}

func (s *headlessStreamState) begin(kind headlessStreamKind) {
	s.kind = kind
	s.text.Reset()
}

func (s *headlessStreamState) end() {
	s.kind = headlessStreamNone
	s.text.Reset()
}

func (s *headlessStreamState) is(kind headlessStreamKind) bool {
	return s.kind == kind
}

func (s *headlessStreamState) active() bool {
	return s.kind != headlessStreamNone
}

// plainRenderer implements HeadlessRenderer for line-oriented, uncolored
type HeadlessApp struct {
	subs     *subsystems
	opts     RuntimeOptions
	renderer HeadlessRenderer
	confirm  ConfirmStrategy

	statsMu               sync.Mutex
	tokenPromptTotal      int
	tokenPredictedTotal   int
	tokenCacheReadTotal   int
	tokenCacheWriteTotal  int
	tokenSessionMax       int
	tokenSessionEstimate  int
	tokenSessionProjected int
	lastTurnPromptN       int
	lastTurnPredictedN    int
	lastTurnCacheRead     int
	lastTurnCacheWrite    int
	lastTurnSpeed         float64
	turnCount             int
	microCompacts         int
	compacts              int
	// compactions documents each completed compression round (per-round
	// session-stats record), mirroring App.compactions so headless summary
	// output can list them. Guarded by statsMu like the counters above.
	compactions    []CompactionRound
	toolCallsTotal int

	stream headlessStreamState

	// toolCallNames maps ToolCallID to ToolName so tool results can be
	// labelled even when the agent event omits the tool name.
	toolCallNames   map[string]string
	toolCallNamesMu sync.Mutex

	// orchErr records a terminal orchestration run failure so it can be
	// reported (and mapped to a non-zero exit code) instead of vanishing
	// inside the run goroutine.
	orchErr   error
	orchErrMu sync.Mutex

	startTime time.Time
}

// NewHeadlessApp creates a headless app from subsystems and options.
func NewHeadlessApp(subs *subsystems, opts RuntimeOptions, renderer HeadlessRenderer, confirm ConfirmStrategy) *HeadlessApp {
	return &HeadlessApp{
		subs:          subs,
		opts:          opts,
		renderer:      renderer,
		confirm:       confirm,
		toolCallNames: make(map[string]string),
		startTime:     time.Now(),
	}
}

// Run executes the headless session and returns an exit code.
func (h *HeadlessApp) Run() int {
	ctx := context.Background()
	var cancel context.CancelFunc
	if h.opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, h.opts.Timeout)
		defer cancel()
	}
	return h.RunWithContext(ctx)
}

// RunWithContext executes the headless session under the provided context and
// returns an exit code. The context can be cancelled by a timeout or a signal
// handler to stop the session early.
func (h *HeadlessApp) RunWithContext(ctx context.Context) int {
	prompt, err := h.opts.UserPrompt()
	if err != nil {
		h.renderer.Error(err.Error())
		return headlessExitConfigError
	}
	if prompt == "" && !h.opts.Goal && h.opts.Orchestrate == "" {
		h.renderer.Error("empty prompt: provide a non-empty prompt with --prompt or use --goal or --orchestrate")
		return headlessExitConfigError
	}
	h.renderer.UserPrompt(prompt)

	if err := h.startSession(); err != nil {
		h.renderer.Error(err.Error())
		return headlessExitProviderError
	}
	// F6: persist session state (mode + companion history) when the headless
	// session ends so agent-driven companion reviews survive in state.json.
	defer h.persistSessionState()

	dc := &doneCloser{done: make(chan struct{})}
	go h.runAgentEventReader(ctx, dc)
	go h.runOrchestratorEventReader(ctx, dc)
	go h.runConfirmationReader(ctx, dc)

	if err := h.startWork(ctx, dc, prompt); err != nil {
		h.renderer.Error(err.Error())
		return headlessExitProviderError
	}

	if code := h.awaitWorkCompletion(ctx, dc); code >= 0 {
		return code
	}

	if h.opts.MaxTurns > 0 && h.turnCount >= h.opts.MaxTurns {
		h.renderer.Error("max turns exceeded")
		return headlessExitMaxTurns
	}

	h.renderer.Summary(h.buildStats(), h.turnCount, time.Since(h.startTime))
	h.renderer.Flush()
	return 0
}

// awaitWorkCompletion blocks until the work signals done or the context is
// cancelled. Returns the exit code, or -1 to continue to normal summary.
func (h *HeadlessApp) awaitWorkCompletion(ctx context.Context, dc *doneCloser) int {
	select {
	case <-dc.done:
		if code := h.reportGoalOutcome(); code != 0 {
			return code
		}
		if code := h.terminalOrchestrateCode(); code != 0 {
			return code
		}
		return -1
	case <-ctx.Done():
		return h.handleContextCancelled(ctx)
	}
}

// startWork begins the requested headless work (goal or single prompt) and
// starts the corresponding completion watcher.
func (h *HeadlessApp) startWork(ctx context.Context, dc *doneCloser, prompt string) error {
	if h.opts.Orchestrate != "" {
		if h.subs.orchAdapter == nil || h.subs.orchActive == nil {
			return fmt.Errorf("orchestrator subsystem not available")
		}
		// The completion watcher must only start after startOrchestrate has
		// installed the runtime: launching it beforehand races with
		// orchActive.Set and can observe a nil runtime, closing dc before the
		// run even starts (the process then exits mid-run).
		rt, err := h.startOrchestrate(ctx, h.opts.Orchestrate, h.subs.orchAdapter)
		if err != nil {
			return err
		}
		go h.waitForOrch(ctx, dc, rt)
		return nil
	}
	if h.opts.Goal {
		go h.waitForGoal(ctx, dc)
		return h.startGoal(ctx, prompt)
	}
	go h.waitForIdle(ctx, dc)
	// Expand @[label](goa-session:<id>) cross-session references (P24 / CX7)
	// the same way the interactive prompt intake does.
	expanded, err := expandSessionReferences(h.subs, prompt)
	if err != nil {
		return fmt.Errorf("session reference error: %w", err)
	}
	if err := h.subs.agentMgr.SendUserInput(expanded); err != nil {
		return fmt.Errorf("send error: %w", err)
	}
	return nil
}

// handleContextCancelled reacts to the provided context being cancelled. It
// interrupts the agent and returns the appropriate exit code.
func (h *HeadlessApp) handleContextCancelled(ctx context.Context) int {
	if h.subs.agentMgr != nil {
		h.subs.agentMgr.Interrupt()
	}
	if h.opts.Timeout > 0 && ctx.Err() == context.DeadlineExceeded {
		h.renderer.Error("session timeout exceeded")
		return headlessExitTimeout
	}
	h.renderer.Error("session cancelled")
	return headlessExitTimeout
}

// startOrchestrate resumes an orchestrator run headless. It replays the run's
// event log to recover topology + objective, builds a Runtime via the builder,
// and re-roots it onto the existing run (same run-id + event log, finished
// roles skipped) via Runtime.Resume — mirroring the TUI /orchestrate:resume
// path. The returned runtime is already installed as the active runtime; the
// caller must start the completion watcher only after this returns. A terminal
// run error is captured in orchErr and reported at summary time.
func (h *HeadlessApp) startOrchestrate(ctx context.Context, runID string, builder orchestrator.Builder) (*orchestrator.Runtime, error) {
	rootDir := filepath.Join(h.subs.projectDir, ".goa", "orchestrator")
	internalID, err := orchestrator.ResolveRunID(rootDir, runID)
	if err != nil {
		return nil, err
	}
	// Replay the very store the runtime will resume onto: Replay advances the
	// store's seq so resumed events continue the existing log instead of
	// restarting at 1.
	store := orchestrator.NewFileEventStore(rootDir, internalID)
	snap, err := orchestrator.ReplaySnapshot(store)
	if err != nil {
		return nil, fmt.Errorf("resume %s: %w", internalID, err)
	}
	if snap.Objective == "" {
		return nil, fmt.Errorf("run %s has no objective to resume", internalID)
	}
	if snap.Finished {
		return nil, fmt.Errorf("run %s is already finished", internalID)
	}
	cfg := h.subs.cfg.Orchestrator
	if snap.Topology != "" {
		cfg.Defaults.Topology = string(snap.Topology)
	}
	rt, err := builder.NewRuntime(cfg, rootDir)
	if err != nil {
		return nil, err
	}
	if snap.Name != "" {
		rt.SetName(snap.Name)
	}
	// Continue the same run (same run-id + event log) and skip roles that
	// already finished, instead of forking a new run under a new id.
	rt.Resume(store, snap)
	// F1: headless runs were goal-less — the goal binder was only wired in the
	// TUI /orchestrate:new path. Bind a goal now (a fresh orchestrator-managed
	// goal when the run has none; adopt the run's existing goal on resume of a
	// goal-bound run) so run_started carries goal_id and the goal lifecycle
	// (complete/block) is driven. A bind failure degrades to goal-less with a
	// warning, mirroring the TUI path.
	if h.subs.goalManager != nil {
		if err := commands.BindGoalToRuntime(rt, h.subs.goalManager.Mode, snap.Objective, snap.Name, snap.GoalID); err != nil {
			h.renderer.Error(fmt.Sprintf("Warning: goal bind failed (%v); running goal-less.", err))
		}
	}
	h.subs.orchActive.Set(rt)
	h.renderer.UserPrompt(fmt.Sprintf("Resuming orchestration [%s]: %s", cfg.Defaults.Topology, snap.Objective))
	go h.forwardOrchEvents(rt)
	go func() {
		h.setOrchErr(rt.Run(ctx, snap.Objective))
	}()
	return rt, nil
}

// setOrchErr records the terminal orchestration error (nil on success).
func (h *HeadlessApp) setOrchErr(err error) {
	h.orchErrMu.Lock()
	h.orchErr = err
	h.orchErrMu.Unlock()
}

// persistSessionState flushes session state (mode, agent-driven flag,
// companion history) to state.json at the end of a headless session.
func (h *HeadlessApp) persistSessionState() {
	if h.subs.agentMgr != nil {
		_ = h.subs.agentMgr.PersistState()
	}
}

// terminalOrchestrateCode maps a recorded orchestration failure to the
// dedicated exit code, reporting it once at summary time.
func (h *HeadlessApp) terminalOrchestrateCode() int {
	h.orchErrMu.Lock()
	err := h.orchErr
	h.orchErrMu.Unlock()
	if err == nil {
		return 0
	}
	h.renderer.Error("orchestration failed: " + err.Error())
	return headlessExitOrchFailed
}

// forwardOrchEvents renders lifecycle events from a run to the headless output.
func (h *HeadlessApp) forwardOrchEvents(rt *orchestrator.Runtime) {
	for ev := range rt.Events() {
		switch ev.Type {
		case orchestrator.EventAgentFinished:
			h.renderer.UserPrompt(fmt.Sprintf("%s: %s", ev.Role, payloadString(ev, "outcome")))
		case orchestrator.EventRunFinished:
			status := "finished"
			if payloadString(ev, "ok") == "false" {
				status = "finished with errors"
			}
			h.renderer.UserPrompt("orchestration " + status)
		}
	}
}

// waitForOrch blocks until the given orchestration run finishes, then closes
// dc. The runtime is passed explicitly (never re-read from orchActive) so the
// watcher cannot observe a not-yet-installed runtime.
func (h *HeadlessApp) waitForOrch(ctx context.Context, dc *doneCloser, rt *orchestrator.Runtime) {
	if rt == nil {
		close(dc.done)
		return
	}
	select {
	case <-rt.Done():
	case <-ctx.Done():
	}
	close(dc.done)
}

func payloadString(ev orchestrator.Event, key string) string {
	if v, ok := ev.Payload[key].(string); ok {
		return v
	}
	return ""
}

// startGoal creates a headless goal from the prompt and starts the goal driver.
func (h *HeadlessApp) startGoal(ctx context.Context, objective string) error {
	if h.subs.goalManager == nil {
		return fmt.Errorf("goal subsystem not available")
	}
	// Creation entry point: fail fast with the point-to-a-markdown-doc hint.
	if err := goal.ValidateObjective(objective); err != nil {
		return err
	}
	snap, err := h.subs.goalManager.Mode.CreateGoal(goal.CreateGoalInput{
		Objective: objective,
		Replace:   true,
	}, goal.GoalActorUser)
	if err != nil {
		return err
	}
	h.renderer.UserPrompt(fmt.Sprintf("Started goal [%s]: %s", snap.Name, snap.Objective))
	if h.subs.goalDriver != nil {
		h.subs.goalDriver.Start(ctx)
	}
	return nil
}

// goalActive reports whether a goal is currently active and should keep the
// headless session running. A paused, completed, or blocked goal is treated as
// inactive so the session exits cleanly instead of waiting forever.
func (h *HeadlessApp) goalActive() bool {
	if h.subs.goalManager == nil {
		return false
	}
	g := h.subs.goalManager.Mode.GetGoal().Goal
	return g != nil && g.Status == goal.GoalActive
}

// reportGoalOutcome emits a clear message when a headless goal finishes so the
// user knows whether it completed, was blocked, or was paused. It returns a
// non-zero exit code when the goal did not complete successfully.
func (h *HeadlessApp) reportGoalOutcome() int {
	if !h.opts.Goal || h.subs.goalManager == nil {
		return 0
	}
	g := h.subs.goalManager.Mode.GetGoal().Goal
	if g == nil {
		return 0
	}
	switch g.Status {
	case goal.GoalDone:
		h.renderer.UserPrompt(fmt.Sprintf("Goal [%s] completed.", g.Name))
	case goal.GoalBlocked:
		reason := "no reason provided"
		if g.TerminalReason != nil && *g.TerminalReason != "" {
			reason = *g.TerminalReason
		}
		h.renderer.Error(fmt.Sprintf("Goal [%s] blocked: %s", g.Name, reason))
		return headlessExitGoalFailed
	case goal.GoalPaused:
		reason := "no reason provided"
		if g.TerminalReason != nil && *g.TerminalReason != "" {
			reason = *g.TerminalReason
		}
		h.renderer.Error(fmt.Sprintf("Goal [%s] paused: %s", g.Name, reason))
		return headlessExitGoalFailed
	}
	return 0
}

// confirmTool forwards tool-approval requests to the headless confirmation
// strategy. It is set as the agent's ConfirmTool callback so ask/confirm
// autonomy levels can still prompt (or auto-approve/reject) in headless mode.
func (h *HeadlessApp) confirmTool(ctx context.Context, toolName, input string) (bool, error) {
	if h.confirm == nil {
		return false, fmt.Errorf("no confirmation strategy configured")
	}
	return h.confirm.Confirm(toolName, input)
}

func (h *HeadlessApp) startSession() error {
	if h.subs.providerMgr == nil {
		return fmt.Errorf("no provider configured")
	}
	providerCfg, _ := h.subs.providerMgr.Active()
	if providerCfg == nil {
		return fmt.Errorf("no provider configured")
	}

	mdl, err := h.subs.providerMgr.ResolveActiveModel()
	if err != nil {
		return fmt.Errorf("failed to resolve model: %w", err)
	}

	// Local servers report the loaded context length only once the model is
	// loaded; refresh before building the system prompt so budgets and
	// compression limits match the actual available context.
	if nCtx := h.subs.providerMgr.RefreshLocalContextWindow(); nCtx > 0 {
		mdl.ContextWindow = nCtx
		h.subs.ContextWindow = nCtx
	}

	streamOpts := h.subs.providerMgr.BuildStreamOptions()
	systemPrompt := buildSystemPrompt(h.subs)
	agenticTools := h.subs.toolRegistry.All()
	// Headless mode reads agent events from the internal channel, so enable
	// forwarding before starting the session.
	h.subs.agentMgr.SetForwardInternalEvents(true)
	// Wire tool confirmation so ask/confirm autonomy levels can prompt (or
	// reject when non-interactive) before executing sensitive tools.
	h.subs.agentMgr.SetConfirmTool(h.confirmTool)
	_, err = h.subs.agentMgr.StartSession(mdl, streamOpts, systemPrompt, agenticTools, h.subs.liveConfig())
	if err != nil {
		return fmt.Errorf("failed to start session: %w", err)
	}

	if h.subs.foregroundOrch != nil {
		if mainAgent := h.subs.agentMgr.CurrentAgent(); mainAgent != nil {
			h.subs.foregroundOrch.SetMainAgent(mainAgent)
		}
	}

	return nil
}

type doneCloser struct {
	done chan struct{}
	once sync.Once
}

func (d *doneCloser) close() {
	d.once.Do(func() { close(d.done) })
}

func (h *HeadlessApp) handleOrchestratorMessage(m multiagent.OrchestratorMessage) {
	switch m.Kind {
	case "thinking_start":
		h.renderer.CompanionThinkingStart()
	case "thinking_chunk":
		h.renderer.CompanionThinkingChunk(m.Content)
	case "thinking_end":
		h.renderer.CompanionThinkingEnd()
	case "content":
		h.handleCompanionContent(m)
	}
}

func (h *HeadlessApp) handleCompanionContent(m multiagent.OrchestratorMessage) {
	switch m.To {
	case "stream_start":
		h.renderer.CompanionChunk("")
	case "stream_chunk":
		h.renderer.CompanionChunk(m.Content)
	case "stream_end":
		// stream_end is handled by the last chunk; nothing to finalize.
	}
}

// runHeadless configures and runs a headless session, exiting the process
// with the returned code.
func runHeadless(subs *subsystems, opts RuntimeOptions) {
	if opts.Yes {
		setHeadlessYolo(subs)
	}
	confirm := resolveConfirmStrategy(opts)
	renderer := resolveRenderer(opts)
	app := NewHeadlessApp(subs, opts, renderer, confirm)

	// Make Ctrl+C (SIGINT) exit the headless session cleanly. Without this,
	// the process keeps running until the turn completes or the timeout fires.
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		select {
		case <-sigCh:
			renderer.Error("interrupted by user (Ctrl+C)")
			renderer.Flush()
			if subs.agentMgr != nil {
				subs.agentMgr.Interrupt()
			}
			cancel()
		case <-ctx.Done():
		}
	}()

	exitCode := app.RunWithContext(ctx)
	cancel()
	os.Exit(exitCode)
}

func setHeadlessYolo(subs *subsystems) {
	if subs.agentMgr != nil {
		subs.agentMgr.SetMode(subs.agentMgr.CurrentMode().WithAutonomy(internal.AutonomyYolo))
	}
	if subs.execCtrl != nil {
		subs.execCtrl.SetMode(internal.ExecutionYolo)
	}
}

func resolveRenderer(opts RuntimeOptions) HeadlessRenderer {
	color := opts.Color
	if color == "" {
		color = "auto"
	}
	forcePlain := opts.Plain || color == "never" || (color == "auto" && !isStdoutTTY())
	if forcePlain {
		return newPlainRenderer(os.Stdout)
	}
	return newANSIRenderer(os.Stdout)
}

func resolveConfirmStrategy(opts RuntimeOptions) ConfirmStrategy {
	if opts.Yes {
		return autoConfirmStrategy{}
	}
	if isStdinTTY() {
		return &ttyConfirmStrategy{in: bufio.NewReader(os.Stdin), out: os.Stdout}
	}
	return &rejectConfirmStrategy{out: os.Stdout}
}

func isStdoutTTY() bool {
	if fi, err := os.Stdout.Stat(); err == nil {
		return (fi.Mode() & os.ModeCharDevice) != 0
	}
	return false
}

func isStdinTTY() bool {
	if fi, err := os.Stdin.Stat(); err == nil {
		return (fi.Mode() & os.ModeCharDevice) != 0
	}
	return false
}
