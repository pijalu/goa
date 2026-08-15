// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/hooks"
	"github.com/pijalu/goa/internal/perms"
	"github.com/pijalu/goa/internal/toolaccess"
)

// SpillPolicy bounds oversized plain-text tool results by saving them
// verbatim to a session-scoped spill dir and substituting a budgeted
// head/tail preview plus a locator notice (gap CX2, dsh spill-policy parity).
// The implementation lives in the tools package; the agent only sees this
// seam. Nil disables the policy.
type SpillPolicy interface {
	// ApplySpill returns the model-facing content for a successful plain-text
	// tool result: the original when the policy does not apply (under cap,
	// read tool, storage failure), or the bounded replacement when spilled.
	ApplySpill(toolName, result string) string
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
	// Surface true execution starts to the UI: a queued task (conflict or
	// MaxParallel) shows "waiting" until the scheduler actually starts it
	// (Bug W). Emitted from scheduler goroutines like tool progress.
	names := make(map[string]string, len(tcs))
	for i := range tcs {
		names[tcs[i].ToolCallID] = tcs[i].ToolName
	}
	sched.OnStart = func(callID string) {
		a.emitEvent(OutputEvent{
			Type:       EventToolStart,
			State:      StateToolCall,
			ToolName:   names[callID],
			ToolCallID: callID,
		})
	}
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
			return a.executeToolWithResult(ctx, tc.ToolName, tc.ToolArguments, tc.ToolCallID)
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

// fileToolNames are the tools whose successful results are treated as
// workspace-instruction touches (gap CX5). Goa's fuzzy edit is part of the
// "edit" tool (AllowFuzz), so the dsh fuzzyedit surface maps to "edit" here.
var fileToolNames = map[string]bool{
	"read":  true,
	"write": true,
	"edit":  true,
}

// injectInstructionLifecycle surfaces workspace-instruction lifecycle changes
// (gap CX5, dsh agent-instructions parity). After every successful
// read/write/edit call in the batch, it asks the tracker to reconcile the
// touched path and appends one durable user-role message per detected change
// (Additional instructions from…, Updated instructions from…, Instructions
// removed:…). The messages are appended to history after the tool results, so
// the next stream round sends them to the model.
func (a *Agent) injectInstructionLifecycle(tcs []provider.ContentBlock, realResults []ToolCallResult) {
	tracker := a.cfg.InstructionTracker
	if tracker == nil || a.cfg.ProjectDir == "" {
		return
	}
	byID := indexResultsByID(realResults)
	var msgs []Message
	for _, tc := range tcs {
		if !fileToolNames[tc.ToolName] {
			continue
		}
		r, ok := byID[tc.ToolCallID]
		if !ok || r.Err != nil {
			continue
		}
		path := fileToolPath(tc.ToolArguments)
		if path == "" {
			continue
		}
		for _, change := range tracker.Reconcile(path) {
			msgs = append(msgs, Message{
				Type:    Content,
				Role:    User,
				Content: internal.RenderInstructionMessage(change),
			})
		}
	}
	if len(msgs) == 0 {
		return
	}
	a.mu.Lock()
	a.history = append(a.history, msgs...)
	a.mu.Unlock()
	for i := range msgs {
		a.emitMessage(msgs[i])
	}
}

// fileToolPath extracts the "path" field from a file-tool JSON input. The
// tracker only needs the directory the touch happened in to compute newly
// reachable scopes, so a best-effort parse is sufficient.
func fileToolPath(input string) string {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return ""
	}
	return p.Path
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
	// Near-duplicate bash re-run (same upstream, only the filter changed, no
	// intervening mutation): append a non-blocking hint teaching the cheaper
	// save-once-refilter pattern. The result itself is untouched.
	if tc.ToolName == "bash" && a.popBashNearDup(tc.ToolCallID) {
		output += nearDuplicateHint
	}
	// Tool-result spill policy (gap CX2): an oversized plain-text result is
	// saved verbatim to the session spill dir and replaced by a budgeted
	// head/tail preview + locator notice. Error results never reach this
	// point (early return above); the policy itself skips read and keeps the
	// original on any storage failure.
	if a.cfg.SpillPolicy != nil {
		if spilled := a.cfg.SpillPolicy.ApplySpill(tc.ToolName, output); spilled != output {
			return spilled
		}
	}
	if limit := a.toolResultSizeLimit(); limit > 0 && len(output) > limit {
		return truncateToolResult(output, limit)
	}
	return output
}

// popBashNearDup reports (and clears) whether the given call was flagged as a
// near-duplicate bash re-run. Lock-safe: the flag map is written under a.mu in
// shouldBufferToolCall while results are resolved without it.
func (a *Agent) popBashNearDup(callID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.bashNearDup[callID] {
		delete(a.bashNearDup, callID)
		return true
	}
	return false
}

// truncateToolResult caps a tool result to roughly limit bytes while preserving
// both the start and the end. The beginning matters for tools like read_file
// (structure/context at the top); the end matters for bash/webfetch (errors and
// final results live at the tail). The middle is elided with a clear marker so
// the model knows content was dropped and can re-read a narrower range. This
// caps tool output at the source so it never inflates the prefix that later
// compaction would have to elide anyway.
func truncateToolResult(output string, limit int) string {
	const markerFmt = "\n[goa-system] Tool result was truncated to ~%d bytes (original %d bytes); the middle was elided, the beginning and end are preserved.\n"
	marker := fmt.Sprintf(markerFmt, limit, len(output))
	half := (limit - len(marker)) / 2
	if half < 1 {
		half = 1
	}
	if len(output) <= half*2+len(marker) {
		return output
	}
	head := output[:half]
	tail := output[len(output)-half:]
	return head + marker + tail
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

func (a *Agent) executeToolWithResult(ctx context.Context, name, input, callID string) (ToolResult, error) {
	if err := a.fireBeforeToolHook(ctx, name, input, callID); err != nil {
		return ToolResult{}, err
	}
	// Inject an execution-progress emitter so streaming-capable tools (those
	// that call agentic.ProgressFromContext) can report partial output while
	// still running. The emitted EventToolProgress events are transient UI
	// updates; the final EventToolResult is still emitted on completion.
	execCtx := ctx
	if callID != "" {
		emit := ProgressFunc(func(partial string) {
			a.emitEvent(OutputEvent{
				Type:       EventToolProgress,
				State:      StateToolCall,
				ToolName:   name,
				ToolCallID: callID,
				Text:       partial,
			})
		})
		execCtx = WithProgress(ctx, emit)
	}
	result, err := a.runTool(execCtx, name, input)
	a.fireAfterToolHook(ctx, name, input, callID, result, err)
	return result, err
}

func (a *Agent) fireBeforeToolHook(ctx context.Context, name, input, callID string) error {
	if a.cfg.HookEngine == nil {
		return nil
	}
	return a.cfg.HookEngine.FireBeforeTool(ctx, hooks.ToolPayload{
		Event:     string(hooks.EventBeforeTool),
		ToolName:  name,
		ToolInput: input,
		CallID:    callID,
	})
}

func (a *Agent) fireAfterToolHook(ctx context.Context, name, input, callID string, result ToolResult, runErr error) {
	if a.cfg.HookEngine == nil {
		return
	}
	payload := hooks.ToolPayload{
		Event:     string(hooks.EventAfterTool),
		ToolName:  name,
		ToolInput: input,
		CallID:    callID,
		Output:    result.Output,
	}
	if runErr != nil {
		payload.Error = runErr.Error()
	}
	_ = a.cfg.HookEngine.FireAfterTool(ctx, payload)
}

// runTool executes the tool and preserves control signals such as StopTurn.
// The turn ctx is forwarded to tools that implement ContextTool so
// long-running/hung tools can be cancelled. Tools implementing ResultTool are
// called directly; otherwise the string output of Execute is wrapped into a
// ToolResult.
func (a *Agent) runTool(ctx context.Context, name, input string) (ToolResult, error) {
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
	// ContextResultTool takes priority: ctx AND control signals (StopTurn).
	if crt, ok := tool.(ContextResultTool); ok {
		return crt.ExecuteContextWithResult(ctx, input)
	}
	// ContextTool next: it lets the tool observe cancellation (but its plain
	// string result carries no StopTurn).
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
