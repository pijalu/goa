// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/perms"
	"github.com/pijalu/goa/internal/toolaccess"
)

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
