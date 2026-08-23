// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tui"
)

func (a *App) handleControlEvent(ev event.ControlEvent) bool {
	switch {
	case ev.StopRequest:
		return a.handleStopRequest()
	case ev.NewSession:
		return a.handleNewSession()
	case ev.RunWizard:
		return a.handleRunWizard()
	case ev.GateApproval != nil:
		return a.handleGateApprovalControl(ev.GateApproval)
	case ev.SteeringInput != nil:
		return a.handleSteeringInputControl(ev.SteeringInput)
	}
	return false
}

func (a *App) handleStopRequest() bool {
	if a.subs.tuiEngine != nil {
		a.subs.tuiEngine.Stop()
	}
	return true
}

func (a *App) handleNewSession() bool {
	if a.subs.chat != nil {
		a.subs.chat.Clear()
	}
	a.clearStats()
	if a.subs.agentMgr != nil {
		a.subs.agentMgr.StopSession()
	}
	// Deliberate transcript reset: wipe the screen + terminal scrollback and
	// reset the compositor watermark so the fresh canvas renders as a first
	// frame. Without this the old scrollback watermark either flashes the old
	// header or pins the window on blanks (see Compositor.Clear).
	if a.subs.tuiEngine != nil {
		a.subs.tuiEngine.ClearTranscript()
	}
	// T3: /new wipes the scrollback an in-flight replay would be emitting into.
	// Cancel the runner and release the render-suppression so the fresh canvas
	// renders immediately; the (now-stale) replay's rows are gone with the wipe.
	if a.subs.replayRunner != nil {
		a.subs.replayRunner.Cancel()
		a.subs.tuiEngine.SetReplaySuppressed(false)
	}
	startAgentSession(a.subs, a.subs.chat)
	if a.subs.tuiEngine != nil {
		a.subs.tuiEngine.RequestRender()
	}
	return true
}

func (a *App) handleRunWizard() bool {
	a.subs.runWizard = true
	if a.subs.tuiEngine != nil {
		a.subs.tuiEngine.Stop()
	}
	return true
}

func (a *App) handleGateApprovalControl(gate *event.GateApproval) bool {
	a.showGateApprovalSelector(gate)
	return true
}

func (a *App) handleSteeringInputControl(si *event.SteeringInput) bool {
	if a.subs.foregroundOrch == nil {
		return true
	}
	a.subs.foregroundOrch.InjectSteering(si.Text)
	if a.subs.steeringChrome != nil {
		a.subs.steeringChrome.Add(si.Text)
	}
	return true
}

func (a *App) showGateApprovalSelector(gate *event.GateApproval) {
	if a.subs.tuiEngine == nil || a.subs.foregroundOrch == nil {
		return
	}
	items := []tui.SelectorItem{
		{Value: "approve", Label: "Approve", Description: "Continue to next stage"},
		{Value: "skip", Label: "Skip", Description: "Skip this stage"},
		{Value: "retry", Label: "Retry", Description: "Re-run this stage"},
	}
	ch := a.subs.tuiEngine.ShowSelector(gate.Prompt, items, "")
	go func() {
		selected := <-ch
		if selected != "" {
			a.subs.foregroundOrch.SubmitGateDecision(multiagent.GateDecision{Action: selected})
		} else {
			// Default to skip on cancel
			a.subs.foregroundOrch.SubmitGateDecision(multiagent.GateDecision{Action: "skip"})
		}
	}()
}

func (a *App) handleFooterEvent(ev event.FooterEvent) {
	switch {
	case ev.ModeChange != nil:
		a.handleModeChangeEvent(ev.ModeChange)
	case ev.ThinkingLevel != nil:
		a.handleThinkingLevelChange(ev.ThinkingLevel)
	case ev.FooterRefresh:
		a.refreshFooterFromConfig()
	case ev.CompanionCycle != nil:
		a.handleCompanionCycleChange(ev.CompanionCycle)
	case ev.WorkflowStatus != nil:
		a.handleWorkflowStatusEvent(ev.WorkflowStatus)
	case ev.WorkflowProgress != nil:
		a.handleWorkflowProgressEvent(ev.WorkflowProgress)
	case ev.MinorMode != nil:
		if a.subs.footer != nil {
			a.subs.footer.SetMinorMode(ev.MinorMode.Mode)
			a.subs.tuiEngine.RequestRender()
		}
	}
}

// refreshFooterFromConfig rebuilds the status bar from the current in-memory
// config. Used when /config changes settings that affect the footer.
func (a *App) refreshFooterFromConfig() {
	if a.subs.footer == nil {
		return
	}
	subs := a.subs
	// Provider/model may have changed: re-evaluate plugin segments (e.g. the
	// quota segment) so the footer does not keep showing the previous
	// provider's cached data. pushPluginSegments preserves the other fields.
	if subs.tuiEngine != nil {
		a.pushPluginSegments(subs.tuiEngine)
	}
	data := tui.FooterData{
		Workdir:                subs.projectDir,
		Model:                  activeModelDisplay(subs),
		Profile:                string(subs.effectiveModeState().Major),
		Mode:                   string(subs.effectiveModeState().Autonomy),
		CompanionModel:         companionModelDisplay(subs),
		Provider:               sessionProviderID(subs),
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	}
	subs.footer.SetData(data)
	// Team badge: SetTeam is the sole writer (preserveFooterTeam keeps it
	// across stats rebuilds) — push the manager's effective state on every
	// full refresh so /team switches and overlays show immediately.
	name, drifted := teamFooterInfo(subs)
	subs.footer.SetTeam(name, drifted)
	// Queue an async render for the updated footer. The render loop (60fps)
	// picks it up within ~16ms, which is fast enough for model label changes.
	subs.tuiEngine.RequestRender()
}

func (a *App) handleChatEvent(ev event.ChatEvent) {
	switch {
	case ev.ClearChat:
		a.clearChat()
	case ev.InterAgent != nil:
		a.handleInterAgentEvent(ev.InterAgent)
	case ev.Flash != nil:
		a.showFlash(ev.Flash)
	case ev.SystemMessage != nil:
		a.showSystemMessage(ev.SystemMessage)
	case ev.ShowOutputModal != nil:
		a.showOutputModal(ev.ShowOutputModal)
	case ev.ShowReviewPager != nil:
		a.showReviewPager(ev.ShowReviewPager)
	case ev.ShowFileReviewPager != nil:
		a.showFileReviewPager(ev.ShowFileReviewPager)
	case ev.ShowPlanPager != nil:
		a.showPlanPager(ev.ShowPlanPager)
	case ev.ShowPlanStatus != nil:
		a.showPlanStatus(ev.ShowPlanStatus)
	case ev.PipelineProgress != nil:
		a.showPipelineProgress(ev.PipelineProgress)
	case ev.SteeringInjected != nil:
		a.handleSteeringInjected(ev.SteeringInjected)
	}
}

// handleSteeringInjected is called when buffered steering input is consumed
// and injected into the conversation as a follow-up user message.
func (a *App) handleSteeringInjected(injected *event.SteeringInput) {
	if injected == nil {
		return
	}
	subs := a.subs
	if subs.steeringChrome != nil {
		subs.steeringChrome.Clear()
	}
	// The consumed steering lands in the transcript as a user message — as if
	// the user had typed and sent it — per the steering-bubble redesign: the
	// pending bubble is bottom chrome (never in scrollback); only the sent
	// text becomes durable history.
	if subs.chat != nil {
		subs.chat.AddUserMessage(injected.Text)
	}
	if subs.tuiEngine != nil {
		subs.tuiEngine.RequestRender()
	}
}

func (a *App) clearChat() {
	if a.subs.chat != nil {
		a.subs.chat.Clear()
	}
}

func (a *App) showFlash(f *event.Flash) {
	if a.subs.chat == nil || f == nil {
		return
	}
	a.subs.chat.AddFlashMessage("⚡ " + f.Text)
}

// showSystemMessage appends a durable system message posted from a background
// goroutine (e.g. the async OAuth flow surfacing its auth URL / device code).
func (a *App) showSystemMessage(m *event.SystemMessage) {
	if a.subs.chat == nil || m == nil || m.Text == "" {
		return
	}
	if m.Preformatted {
		a.subs.chat.AddSystemMessagePreformatted(m.Text)
	} else {
		a.subs.chat.AddSystemMessage(m.Text)
	}
	if a.subs.tuiEngine != nil {
		a.subs.tuiEngine.RequestRender()
	}
}

func (a *App) showOutputModal(m *event.ShowOutputModal) {
	if a.subs.chat == nil || m == nil || m.Content == "" {
		return
	}
	a.subs.chat.AddSystemMessage(m.Title + ":\n" + m.Content)
}

func (a *App) showReviewPager(m *event.ShowReviewPager) {
	if a.subs.tuiEngine == nil || m == nil || m.Pager == nil {
		return
	}
	pager, ok := m.Pager.(*tui.ReviewPager)
	if !ok {
		return
	}
	opts := a.setupReviewPager(pager)
	// Wire callbacks before showing the overlay so that input events that
	// arrive immediately (e.g. the user already pressing 'q') are handled.
	var handle *tui.OverlayHandle
	a.wireReviewPagerCallbacks(pager, &handle, opts)
	a.showReviewOverlay(pager, &handle, opts)
}
