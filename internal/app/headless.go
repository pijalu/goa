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
	"strings"
	"sync"
	"time"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
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
// output with explicit boundary markers.
type plainRenderer struct {
	out io.Writer

	mu                    sync.Mutex
	assistantOpen         bool
	thinkingOpen          bool
	companionOpen         bool
	companionThinkingOpen bool
}

func newPlainRenderer(out io.Writer) *plainRenderer {
	return &plainRenderer{out: out}
}

func (r *plainRenderer) UserPrompt(prompt string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeOpenBlocksLocked()
	fmt.Fprintln(r.out, "-- user")
	fmt.Fprintln(r.out, prompt)
	fmt.Fprintln(r.out)
}

func (r *plainRenderer) AssistantChunk(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.assistantOpen {
		r.closeOpenBlocksLocked()
		fmt.Fprintln(r.out, "-- assistant")
		r.assistantOpen = true
	}
	fmt.Fprint(r.out, text)
}

func (r *plainRenderer) ThinkingStart() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeOpenBlocksLocked()
	fmt.Fprintln(r.out, "-- thinking start")
	r.thinkingOpen = true
}

func (r *plainRenderer) ThinkingChunk(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.thinkingOpen {
		r.closeOpenBlocksLocked()
		fmt.Fprintln(r.out, "-- thinking start")
		r.thinkingOpen = true
	}
	fmt.Fprint(r.out, text)
}

func (r *plainRenderer) ThinkingEnd() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.thinkingOpen {
		return
	}
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "-- thinking end")
	r.thinkingOpen = false
}

func (r *plainRenderer) ToolCall(name, id, input string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeOpenBlocksLocked()
	fmt.Fprintf(r.out, "-- tool call %s id=%s\n", name, id)
	fmt.Fprintln(r.out, input)
	fmt.Fprintln(r.out)
}

func (r *plainRenderer) ToolResult(name, id, output string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeOpenBlocksLocked()
	fmt.Fprintf(r.out, "-- tool result %s id=%s\n", name, id)
	fmt.Fprintln(r.out, output)
	fmt.Fprintln(r.out)
}

func (r *plainRenderer) Stats(stats sessionStats, turn int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeOpenBlocksLocked()
	fmt.Fprintf(r.out, "-- stats turn=%d %s\n", turn, formatFooterStatsPlain(stats))
}

func (r *plainRenderer) Summary(stats sessionStats, turns int, totalTime time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeOpenBlocksLocked()
	parts := []string{
		fmt.Sprintf("turns=%d", turns),
		fmt.Sprintf("total_in=%d", stats.PromptN),
		fmt.Sprintf("total_out=%d", stats.PredictedN),
		fmt.Sprintf("total_tool_calls=%d", stats.ToolCalls),
	}
	if stats.ShowCost {
		parts = append(parts, fmt.Sprintf("total_cost=$%.4f", stats.CostUSD))
	}
	parts = append(parts, fmt.Sprintf("total_time=%s", totalTime.Round(time.Millisecond)))
	fmt.Fprintf(r.out, "-- summary %s\n", strings.Join(parts, " "))
}

func (r *plainRenderer) Error(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeOpenBlocksLocked()
	fmt.Fprintf(r.out, "-- error\n%s\n", msg)
}

func (r *plainRenderer) AssistantStreamEnd() {
	// Boundary is handled by closeOpenBlocksLocked before the next block.
}

func (r *plainRenderer) CompanionStart(cycle int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeOpenBlocksLocked()
	fmt.Fprintf(r.out, "-- companion start cycle=%d\n", cycle)
	r.companionOpen = true
}

func (r *plainRenderer) CompanionEnd(cycle int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.companionOpen {
		return
	}
	fmt.Fprintf(r.out, "-- companion end cycle=%d\n", cycle)
	r.companionOpen = false
}

func (r *plainRenderer) CompanionThinkingStart() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.companionOpen {
		return
	}
	if !r.companionThinkingOpen {
		fmt.Fprintln(r.out, "-- companion thinking start")
		r.companionThinkingOpen = true
	}
}

func (r *plainRenderer) CompanionThinkingChunk(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.companionOpen {
		return
	}
	if !r.companionThinkingOpen {
		fmt.Fprintln(r.out, "-- companion thinking start")
		r.companionThinkingOpen = true
	}
	fmt.Fprint(r.out, text)
}

func (r *plainRenderer) CompanionThinkingEnd() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.companionThinkingOpen {
		return
	}
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "-- companion thinking end")
	r.companionThinkingOpen = false
}

func (r *plainRenderer) CompanionChunk(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.companionOpen {
		return
	}
	fmt.Fprintln(r.out, "-- companion")
	fmt.Fprint(r.out, text)
}

func (r *plainRenderer) Flush() {
	if f, ok := r.out.(interface{ Sync() error }); ok {
		_ = f.Sync()
	}
}

func (r *plainRenderer) closeOpenBlocksLocked() {
	if r.assistantOpen {
		fmt.Fprintln(r.out)
		r.assistantOpen = false
	}
	if r.thinkingOpen {
		fmt.Fprintln(r.out)
		fmt.Fprintln(r.out, "-- thinking end")
		r.thinkingOpen = false
	}
	if r.companionThinkingOpen {
		fmt.Fprintln(r.out)
		fmt.Fprintln(r.out, "-- companion thinking end")
		r.companionThinkingOpen = false
	}
}

// ansiRenderer implements HeadlessRenderer with minimal ANSI color markers.
type ansiRenderer struct {
	out io.Writer
}

func newANSIRenderer(out io.Writer) *ansiRenderer {
	return &ansiRenderer{out: out}
}

func (r *ansiRenderer) UserPrompt(prompt string) {
	r.colorLine("User:", "user")
	fmt.Fprintln(r.out, prompt)
	fmt.Fprintln(r.out)
}

func (r *ansiRenderer) AssistantChunk(text string) {
	fmt.Fprint(r.out, text)
}

func (r *ansiRenderer) ThinkingStart() {
	r.colorLine("Thinking:", "thinking")
}

func (r *ansiRenderer) ThinkingChunk(text string) {
	fmt.Fprint(r.out, text)
}

func (r *ansiRenderer) ThinkingEnd() {
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out)
}

func (r *ansiRenderer) ToolCall(name, id, input string) {
	r.colorLine(fmt.Sprintf("Tool call %s (id=%s):", name, id), "tool")
	fmt.Fprintln(r.out, input)
	fmt.Fprintln(r.out)
}

func (r *ansiRenderer) ToolResult(name, id, output string) {
	r.colorLine(fmt.Sprintf("Tool result %s (id=%s):", name, id), "tool")
	fmt.Fprintln(r.out, output)
	fmt.Fprintln(r.out)
}

func (r *ansiRenderer) Stats(stats sessionStats, turn int) {
	r.colorLine(fmt.Sprintf("Turn %d stats:", turn), "stats")
	fmt.Fprintln(r.out, formatFooterStats(stats))
}

func (r *ansiRenderer) Summary(stats sessionStats, turns int, totalTime time.Duration) {
	r.colorLine("Summary:", "stats")
	parts := []string{
		fmt.Sprintf("turns=%d", turns),
		fmt.Sprintf("total_in=%d", stats.PromptN),
		fmt.Sprintf("total_out=%d", stats.PredictedN),
		fmt.Sprintf("total_tool_calls=%d", stats.ToolCalls),
	}
	if stats.ShowCost {
		parts = append(parts, fmt.Sprintf("total_cost=$%.4f", stats.CostUSD))
	}
	parts = append(parts, fmt.Sprintf("total_time=%s", totalTime.Round(time.Millisecond)))
	fmt.Fprintln(r.out, strings.Join(parts, " "))
}

func (r *ansiRenderer) Error(msg string) {
	r.colorLine("Error:", "error")
	fmt.Fprintln(r.out, msg)
}

func (r *ansiRenderer) AssistantStreamEnd() {
	fmt.Fprintln(r.out)
}

func (r *ansiRenderer) CompanionStart(cycle int) {
	r.colorLine(fmt.Sprintf("Companion cycle %d:", cycle), "companion")
}

func (r *ansiRenderer) CompanionEnd(cycle int) {
	fmt.Fprintf(r.out, "End companion cycle %d\n\n", cycle)
}

func (r *ansiRenderer) CompanionThinkingStart() {
	r.colorLine("Companion thinking:", "thinking")
}

func (r *ansiRenderer) CompanionThinkingChunk(text string) {
	fmt.Fprint(r.out, text)
}

func (r *ansiRenderer) CompanionThinkingEnd() {
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out)
}

func (r *ansiRenderer) CompanionChunk(text string) {
	r.colorLine("Companion:", "companion")
	fmt.Fprint(r.out, text)
}

func (r *ansiRenderer) Flush() {
	if f, ok := r.out.(interface{ Sync() error }); ok {
		_ = f.Sync()
	}
}

func (r *ansiRenderer) colorLine(label, role string) {
	color := ansi.Fg(roleColor(role))
	fmt.Fprintf(r.out, "%s%s%s\n", color, label, ansi.Reset)
}

func roleColor(role string) string {
	switch role {
	case "user":
		return "#58a6ff"
	case "assistant":
		return "#7ee787"
	case "thinking":
		return "#d2a8ff"
	case "tool":
		return "#ffa657"
	case "stats":
		return "#8b949e"
	case "error":
		return "#f85149"
	case "companion":
		return "#79c0ff"
	default:
		return "#c9d1d9"
	}
}

// HeadlessApp runs a single headless agent session.
type HeadlessApp struct {
	subs     *subsystems
	opts     RuntimeOptions
	renderer HeadlessRenderer
	confirm  ConfirmStrategy

	statsMu              sync.Mutex
	tokenPromptTotal     int
	tokenPredictedTotal  int
	tokenCacheReadTotal  int
	tokenCacheWriteTotal int
	tokenSessionMax      int
	tokenSessionMaxAuto  bool
	tokenSessionEstimate int
	lastTurnPromptN      int
	lastTurnPredictedN   int
	lastTurnCacheRead    int
	lastTurnCacheWrite   int
	lastTurnSpeed        float64
	turnCount            int
	microCompacts        int
	compacts             int
	toolCallsTotal       int

	stream headlessStreamState

	// toolCallNames maps ToolCallID to ToolName so tool results can be
	// labelled even when the agent event omits the tool name.
	toolCallNames   map[string]string
	toolCallNamesMu sync.Mutex

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
	h.renderer.UserPrompt(prompt)

	if err := h.startSession(); err != nil {
		h.renderer.Error(err.Error())
		return headlessExitProviderError
	}

	dc := &doneCloser{done: make(chan struct{})}
	go h.runAgentEventReader(ctx, dc)
	go h.runOrchestratorEventReader(ctx, dc)
	go h.runConfirmationReader(ctx, dc)

	if err := h.startWork(ctx, dc, prompt); err != nil {
		h.renderer.Error(err.Error())
		return headlessExitProviderError
	}

	select {
	case <-dc.done:
		if code := h.reportGoalOutcome(); code != 0 {
			return code
		}
	case <-ctx.Done():
		return h.handleContextCancelled(ctx)
	}

	if h.opts.MaxTurns > 0 && h.turnCount >= h.opts.MaxTurns {
		h.renderer.Error("max turns exceeded")
		return headlessExitMaxTurns
	}

	h.renderer.Summary(h.buildStats(), h.turnCount, time.Since(h.startTime))
	h.renderer.Flush()
	return 0
}

// startWork begins the requested headless work (goal or single prompt) and
// starts the corresponding completion watcher.
func (h *HeadlessApp) startWork(ctx context.Context, dc *doneCloser, prompt string) error {
	if h.opts.Goal {
		go h.waitForGoal(ctx, dc)
		return h.startGoal(ctx, prompt)
	}
	go h.waitForIdle(ctx, dc)
	if err := h.subs.agentMgr.SendUserInput(prompt); err != nil {
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

// startGoal creates a headless goal from the prompt and starts the goal driver.
func (h *HeadlessApp) startGoal(ctx context.Context, objective string) error {
	if h.subs.goalManager == nil {
		return fmt.Errorf("goal subsystem not available")
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

	streamOpts := h.subs.providerMgr.BuildStreamOptions()
	systemPrompt := buildSystemPrompt(h.subs)
	agenticTools := h.subs.toolRegistry.All()
	// Headless mode reads agent events from the internal channel, so enable
	// forwarding before starting the session.
	h.subs.agentMgr.SetForwardInternalEvents(true)
	// Wire tool confirmation so ask/confirm autonomy levels can prompt (or
	// reject when non-interactive) before executing sensitive tools.
	h.subs.agentMgr.SetConfirmTool(h.confirmTool)
	_, err = h.subs.agentMgr.StartSession(mdl, streamOpts, systemPrompt, agenticTools, h.subs.cfg)
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

func (h *HeadlessApp) runAgentEventReader(ctx context.Context, dc *doneCloser) {
	defer dc.close()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-h.subs.agentMgr.Events():
			if !ok {
				return
			}
			h.handleAgentEvent(&ev)
		}
	}
}

func (h *HeadlessApp) runOrchestratorEventReader(ctx context.Context, dc *doneCloser) {
	if h.subs.foregroundOrch == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-dc.done:
			return
		case m, ok := <-h.subs.foregroundOrch.Events():
			if !ok {
				return
			}
			h.handleOrchestratorMessage(m)
		}
	}
}

func (h *HeadlessApp) waitForIdle(ctx context.Context, dc *doneCloser) {
	if h.subs.agentMgr == nil {
		dc.close()
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
			if !h.subs.agentMgr.IsRunning() {
				// Give in-flight events a moment to drain before signaling done.
				time.Sleep(100 * time.Millisecond)
				dc.close()
				return
			}
		}
	}
}

// waitForGoal waits until there is no active goal and the agent is idle.
// The GoalDriver runs continuation turns in the background; this loop avoids
// exiting while a turn is in flight or while the goal can still continue.
func (h *HeadlessApp) waitForGoal(ctx context.Context, dc *doneCloser) {
	if h.subs.agentMgr == nil || h.subs.goalManager == nil {
		dc.close()
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
			if !h.goalActive() && !h.subs.agentMgr.IsRunning() {
				// Give in-flight events a moment to drain before signaling done.
				time.Sleep(100 * time.Millisecond)
				dc.close()
				return
			}
		}
	}
}

func (h *HeadlessApp) runConfirmationReader(ctx context.Context, dc *doneCloser) {
	if h.subs.execCtrl == nil {
		return
	}
	consumer := func(c context.Context, req internal.ConfirmRequest) error {
		approved, err := h.confirm.Confirm(req.ToolName, req.ToolInput)
		if err != nil {
			select {
			case req.ResponseChan <- internal.ConfirmNo:
			case <-c.Done():
			}
			return err
		}
		resp := internal.ConfirmNo
		if approved {
			resp = internal.ConfirmYes
		}
		select {
		case req.ResponseChan <- resp:
		case <-c.Done():
			return c.Err()
		}
		return nil
	}
	h.subs.execCtrl.SetConfirmConsumer(func(c context.Context, req internal.ConfirmRequest) error {
		select {
		case <-dc.done:
			return context.Canceled
		case <-ctx.Done():
			return ctx.Err()
		default:
			return consumer(c, req)
		}
	})
	// Keep the consumer registered until the session is done or the context
	// is cancelled. This avoids the legacy queue-based race where a tool call
	// would default to ConfirmYes if no listener was immediately ready.
	select {
	case <-dc.done:
	case <-ctx.Done():
	}
}

func (h *HeadlessApp) handleAgentEvent(ev *agentic.OutputEvent) {
	switch ev.Type {
	case agentic.EventContent:
		h.handleContentEvent(ev)
	case agentic.EventToolCall:
		h.handleToolCallEvent(ev)
	case agentic.EventToolResult:
		h.handleToolResultEvent(ev)
	case agentic.EventEnd:
		h.handleEndEvent(ev)
	case agentic.EventTokenStats, agentic.EventContextStats:
		h.handleStatsEvent(ev)
	case agentic.EventCompact:
		h.recordCompact(ev.Text)
	case agentic.EventProgress:
		// Progress/status messages are not rendered in headless mode.
	}
}

func (h *HeadlessApp) handleContentEvent(ev *agentic.OutputEvent) {
	if ev.Role == agentic.User || ev.Role == agentic.System {
		return
	}
	switch ev.State {
	case agentic.StateThinking:
		if !h.stream.is(headlessStreamThinking) {
			if h.stream.active() {
				h.endStream()
			}
			h.renderer.ThinkingStart()
			h.stream.begin(headlessStreamThinking)
		}
		h.renderer.ThinkingChunk(ev.Text)
		h.stream.text.WriteString(ev.Text)
	default:
		if h.stream.is(headlessStreamThinking) {
			h.renderer.ThinkingEnd()
			h.stream.end()
		}
		if !h.stream.is(headlessStreamAssistant) {
			if h.stream.active() {
				h.endStream()
			}
			h.stream.begin(headlessStreamAssistant)
		}
		h.renderer.AssistantChunk(ev.Text)
		h.stream.text.WriteString(ev.Text)
	}
}

func (h *HeadlessApp) handleToolCallEvent(ev *agentic.OutputEvent) {
	if h.stream.active() {
		h.endStream()
	}
	if ev.ToolCallID != "" {
		h.toolCallNamesMu.Lock()
		h.toolCallNames[ev.ToolCallID] = ev.ToolName
		h.toolCallNamesMu.Unlock()
	}
	h.statsMu.Lock()
	h.toolCallsTotal++
	h.statsMu.Unlock()
	h.renderer.ToolCall(ev.ToolName, ev.ToolCallID, ev.ToolInput)
}

func (h *HeadlessApp) handleToolResultEvent(ev *agentic.OutputEvent) {
	if h.stream.active() {
		h.endStream()
	}
	toolName := ev.ToolName
	if toolName == "" && ev.ToolCallID != "" {
		h.toolCallNamesMu.Lock()
		toolName = h.toolCallNames[ev.ToolCallID]
		h.toolCallNamesMu.Unlock()
	}
	h.renderer.ToolResult(toolName, ev.ToolCallID, ev.Text)
}

func (h *HeadlessApp) handleEndEvent(ev *agentic.OutputEvent) {
	if h.stream.active() {
		h.endStream()
	}

	h.statsMu.Lock()
	h.turnCount++
	stats := h.buildStatsLocked()
	turn := h.turnCount
	exceeded := h.opts.MaxTurns > 0 && h.turnCount >= h.opts.MaxTurns
	h.statsMu.Unlock()

	h.renderer.Stats(stats, turn)

	if ev != nil && ev.Text != "" {
		h.renderer.Error(friendlyConnectionHint(ev.Text))
	} else if ev != nil && ev.Metadata["cancelled"] == "true" {
		h.renderer.Error("Generation stopped by user.")
	}

	if exceeded && h.subs.agentMgr != nil {
		h.subs.agentMgr.Interrupt()
	}
}

func (h *HeadlessApp) handleStatsEvent(ev *agentic.OutputEvent) {
	h.statsMu.Lock()
	defer h.statsMu.Unlock()
	if ev.Timings != nil {
		h.lastTurnPromptN = ev.Timings.PromptN
		h.lastTurnPredictedN = ev.Timings.PredictedN
		h.tokenPromptTotal += ev.Timings.PromptN
		h.tokenPredictedTotal += ev.Timings.PredictedN
		h.lastTurnCacheRead = ev.Timings.CacheReadTokens
		h.lastTurnCacheWrite = ev.Timings.CacheWriteTokens
		h.tokenCacheReadTotal += ev.Timings.CacheReadTokens
		h.tokenCacheWriteTotal += ev.Timings.CacheWriteTokens
		h.lastTurnSpeed = ev.Timings.PredictedPerSecond
		if h.lastTurnSpeed == 0 && ev.Timings.PredictedMs > 0 {
			h.lastTurnSpeed = float64(ev.Timings.PredictedN) / (ev.Timings.PredictedMs / 1000.0)
		}
	}
	if ev.ContextStats != nil {
		h.tokenSessionMax = ev.ContextStats.MaxTokens
		h.tokenSessionMaxAuto = ev.ContextStats.AutoMax
		h.tokenSessionEstimate = ev.ContextStats.EstimatedTokens
	}
}

func (h *HeadlessApp) recordCompact(kind string) {
	h.statsMu.Lock()
	defer h.statsMu.Unlock()
	if kind == "micro" {
		h.microCompacts++
	} else {
		h.compacts++
	}
}

func (h *HeadlessApp) endStream() {
	if h.stream.is(headlessStreamThinking) {
		h.renderer.ThinkingEnd()
	} else if h.stream.is(headlessStreamAssistant) {
		h.renderer.AssistantStreamEnd()
	}
	h.stream.end()
}

func (h *HeadlessApp) buildStats() sessionStats {
	h.statsMu.Lock()
	defer h.statsMu.Unlock()
	return h.buildStatsLocked()
}

func (h *HeadlessApp) buildStatsLocked() sessionStats {
	st := sessionStats{
		PromptN:         h.tokenPromptTotal,
		PredictedN:      h.tokenPredictedTotal,
		SpeedTokPerSec:  h.lastTurnSpeed,
		ContextEstimate: h.tokenSessionEstimate,
		ContextMax:      h.tokenSessionMax,
		ContextAutoMax:  h.tokenSessionMaxAuto,
		ToolCalls:       h.toolCallsTotal,
		MicroCompacts:   h.microCompacts,
		Compacts:        h.compacts,
	}
	applyPricing(&st, h.subs.cfg, h.subs.cfg.ActiveModel)
	return st
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
