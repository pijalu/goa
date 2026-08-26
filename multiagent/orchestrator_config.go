// SPDX-License-Identifier: GPL-3.0-or-later

package multiagent

import (
	"context"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/prompts"
)

// SetRoleRecorder installs the per-role session recorder. Every event
// observed from a pool sub-agent is appended to that role's JSONL file so
// exports and post-mortems see the complete multi-agent exchange.
func (o *ForegroundOrchestrator) SetRoleRecorder(rr *RoleSessionRecorder) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.roleRecorder = rr
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
