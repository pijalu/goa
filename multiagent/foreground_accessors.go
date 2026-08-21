// SPDX-License-Identifier: GPL-3.0-or-later
package multiagent

import (
	"strings"
	"time"
)

const DefaultCompanionEndDelimiter = "</end>"

func (o *ForegroundOrchestrator) Stop() {
	o.stopMu.Lock()
	defer o.stopMu.Unlock()
	o.stopped = true
	select {
	case o.gateCh <- GateDecision{Action: "skip"}:
	default:
		{
		}
	}
}
func (o *ForegroundOrchestrator) Stopped() bool {
	o.stopMu.Lock()
	defer o.stopMu.Unlock()
	return o.stopped
}
func (o *ForegroundOrchestrator) SubmitGateDecision(d GateDecision) {
	select {
	case o.gateCh <- d:
	default:
		{
		}
	}
}
func (o *ForegroundOrchestrator) ActiveRun() *PipelineRun {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.activeRun
}
func (o *ForegroundOrchestrator) ActivePipeline() *Pipeline {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.activePipeline
}
func (o *ForegroundOrchestrator) AccumulatedContext() string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.accumulatedContext
}
func (o *ForegroundOrchestrator) StageToolCount() int     { return int(o.stageToolCount.Load()) }
func (o *ForegroundOrchestrator) SetStageToolCount(n int) { o.stageToolCount.Store(int32(n)) }
func (o *ForegroundOrchestrator) SuspendCompanion() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.mode == WorkflowCompanionMinor {
		o.savedMode = o.mode
		o.mode = WorkflowInactive
	}
}
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
func (o *ForegroundOrchestrator) waitForGateApproval() string {
	select {
	case d := <-o.gateCh:
		return d.Action
	case <-time.After(30 * time.Minute):
		return "skip"
	}
}
func (o *ForegroundOrchestrator) checkSteering() (string, bool) {
	o.mu.Lock()
	sq := o.steerQueue
	o.mu.Unlock()
	if sq == nil {
		return "", false
	}
	p := sq.Flush()
	if len(p) == 0 {
		return "", false
	}
	return strings.Join(p, "\n\n"), true
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
func (o *ForegroundOrchestrator) emit(from, to, content string) {
	o.emitKind(from, to, content, "content")
}
func (o *ForegroundOrchestrator) emitKind(from, to, content, kind string) {
	msg := OrchestratorMessage{From: from, To: to, Content: content, Kind: kind, Timestamp: time.Now()}
	if to == "stream_chunk" {
		select {
		case o.events <- msg:
		default:
			{
			}
		}
		return
	}
	o.events <- msg
}
