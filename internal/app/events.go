// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/internal/review"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tui"
	goaltui "github.com/pijalu/goa/tui/goal"
)

func (a *App) setupEventHandlers(engine *tui.TUI, chat *tui.ChatViewport, inp *tui.Editor) chan struct{} {
	inp.SetOnSubmit(a.makeSubmitHandler(engine, chat))
	inp.OnImagePaste = func(path string) {
		a.handlePastedImage(engine, chat, path)
	}
	done := make(chan struct{})

	bus := a.subs.events
	go a.runAgentEventReader(done, bus.Agent)
	go a.runControlEventReader(done, bus.Control)
	go a.runChatEventReader(done, bus.Chat)
	go a.runFooterEventReader(done, bus.Footer)

	// Forward foreground orchestrator events to the TUI event bus, so that
	// companion post-turn output and other orchestrator-managed workflows
	// show agent-colored messages in the chat viewport.
	if a.subs.foregroundOrch != nil {
		go a.runOrchestratorEventForwarder(done)
	}
	// Forward pipeline runner events once, centrally. Per-command consumers
	// would compete for the same channel and lose events round-robin.
	if a.subs.pipelineRunner != nil {
		go a.runPipelineEventForwarder(done)
	}
	// Persistent multi-agent run view: shows the tabbed (Stats + per-agent +
	// All) view for the active orchestration run, updated on the commandLoop
	// (R1 single-owner invariant). Unlike the old overlay it stays after finish.
	if a.subs.orchActive != nil {
		go a.runOrchestratorViewForwarder(done)
	}

	go func() {
		// Block until either the engine stops (via Ctrl+C) or done is
		// externally closed — whichever happens first. The select prevents
		// busy-polling (see Bug #1 in TOFIX.md).
		select {
		case <-engine.Stopped():
		case <-done:
		}
		// If done was already closed by someone else, don't close it again.
		select {
		case <-done:
		default:
			close(done)
		}
	}()
	return done
}

// apply routes a state-mutating function through the TUI commandLoop (the sole
// state owner in the Actor model). If no TUI engine is attached (headless /
// tests), it runs inline. All event handlers that mutate TUI components must
// go through apply so the commandLoop stays the sole mutator.
func (a *App) apply(fn func()) {
	if a.subs.tuiEngine != nil {
		a.subs.tuiEngine.Apply(fn)
		return
	}
	fn()
}

func (a *App) runAgentEventReader(done chan struct{}, ch <-chan event.AgentEvent) {
	runWithPanicRestart(readerMaxRestarts,
		func(r any, stack []byte) {
			log.Printf("[events] runAgentEventReader panicked: %v\n%s", r, stack)
			// Recover from rendering panics so the agent event loop survives.
			// Without this, a single bad render kills all agent output delivery.
			a.showPanicError("render", r, stack)
		},
		func() {
			log.Printf("[events] runAgentEventReader exceeded %d consecutive restarts; stopping", readerMaxRestarts)
			a.showPanicError("render",
				fmt.Errorf("render loop repeatedly panicked (%d consecutive times)", readerMaxRestarts),
				debug.Stack())
		},
		func() {
			for {
				select {
				case <-done:
					return
				case ev, ok := <-ch:
					if !ok {
						return
					}
					a.apply(func() {
						a.handleAgentOutputEvent(&ev.Event)
						if ev.GoalUpdate != nil {
							a.handleGoalUpdate(ev.GoalUpdate)
						}
					})
				}
			}
		})
}

func (a *App) runControlEventReader(done chan struct{}, ch <-chan event.ControlEvent) {
	for {
		select {
		case <-done:
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
						a.apply(func() {
				if a.handleControlEvent(ev) {
					return
				}
			})
		}
	}
}

func (a *App) runChatEventReader(done chan struct{}, ch <-chan event.ChatEvent) {
	runWithPanicRestart(readerMaxRestarts,
		func(r any, stack []byte) {
			log.Printf("[events] runChatEventReader panicked: %v\n%s", r, stack)
			a.showPanicError("chat", r, stack)
		},
		func() {
			log.Printf("[events] runChatEventReader exceeded %d consecutive restarts; stopping", readerMaxRestarts)
			a.showPanicError("chat",
				fmt.Errorf("chat loop repeatedly panicked (%d consecutive times)", readerMaxRestarts),
				debug.Stack())
		},
		func() {
			for {
				select {
				case <-done:
					return
				case ev, ok := <-ch:
					if !ok {
						return
					}
					a.apply(func() {
						a.handleChatEvent(ev)
					})
				}
			}
		})
}

func (a *App) runFooterEventReader(done chan struct{}, ch <-chan event.FooterEvent) {
	for {
		select {
		case <-done:
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
						a.apply(func() {
				a.handleFooterEvent(ev)
			})
		}
	}
}

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
	a.subs.chat.AddSteeringPending(si.Text)
	if a.subs.footer != nil {
		a.subs.footer.SetData(tui.FooterData{SteeringPending: si.Text})
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
	data := tui.FooterData{
		Workdir:                subs.projectDir,
		Model:                  activeModelDisplay(subs),
		Profile:                string(subs.effectiveModeState().Major),
		Mode:                   string(subs.effectiveModeState().Autonomy),
		CompanionModel:         companionModelDisplay(subs),
		Provider:               subs.cfg.ActiveProvider,
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	}
	subs.footer.SetData(data)
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
	case ev.ShowOutputModal != nil:
		a.showOutputModal(ev.ShowOutputModal)
	case ev.ShowReviewPager != nil:
		a.showReviewPager(ev.ShowReviewPager)
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
	if subs.chat != nil {
		subs.chat.ClearSteeringPending()
		subs.chat.AddSystemMessage(fmt.Sprintf("[steering injected] %s", injected.Text))
	}
	if subs.footer != nil {
		data := subs.footer.Data()
		data.SteeringPending = ""
		subs.footer.SetData(data)
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

// reviewOverlayGeometry holds the computed size and position for the review
// overlay. The overlay occupies the chat-history area, leaving the title
// (header), status bar (footer) and input line visible.
type reviewOverlayGeometry struct {
	width        int
	height       int
	bottomOffset int
}

func (a *App) setupReviewPager(pager *tui.ReviewPager) reviewOverlayGeometry {
	pager.RequestRender = func() {
		a.subs.tuiEngine.RequestRender()
	}
	w := a.subs.tuiEngine.TerminalCols()
	h := a.subs.tuiEngine.TerminalRows()
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	geo := reviewOverlayGeometryFor(h)
	pager.SetViewport(w, geo.height)
	return geo
}

// reviewOverlayGeometryFor reserves zero rows at the top (the review pager
// provides its own title line) and five rows at the bottom for the full
// input editor (3 lines: top border, content line, bottom border) plus the
// status bar (2 lines). This leaves the input's separator line visible so
// the key-binding help can be shown there.
func reviewOverlayGeometryFor(termH int) reviewOverlayGeometry {
	const topReserved = 0
	const bottomReserved = 5
	usable := termH - topReserved - bottomReserved
	if usable < 5 {
		// Terminal too small: fall back to full screen so the pager is still
		// usable.
		usable = termH
	}
	return reviewOverlayGeometry{
		width:        0,
		height:       usable,
		bottomOffset: bottomReserved,
	}
}

const reviewHelpTitle = "q:close ↑↓:scroll c:comment e:edit d:delete b:base s:submit x:export"

func (a *App) showReviewOverlay(pager *tui.ReviewPager, handlePtr **tui.OverlayHandle, geo reviewOverlayGeometry) {
	a.reviewShowOverlay(pager, handlePtr, geo)
}

// reviewSetTitle sets the input line's separator title from inside the
// app goroutine. It is a nil-safe no-op when the input is not ready.
func (a *App) reviewSetTitle(title string) {
	if inp := a.subs.getInput(); inp != nil {
		inp.SetTitle(title)
	}
}

// reviewShowOverlayWithHelp shows the review overlay and sets the input title.
func (a *App) reviewShowOverlay(pager *tui.ReviewPager, handlePtr **tui.OverlayHandle, geo reviewOverlayGeometry) {
	*handlePtr = a.subs.tuiEngine.ShowOverlay(pager, tui.OverlayOptions{
		Width:        geo.width,
		Height:       geo.height,
		BottomOffset: geo.bottomOffset,
		CaptureInput: true,
	})
	a.reviewSetTitle(reviewHelpTitle)
}

func (a *App) wireReviewPagerCallbacks(pager *tui.ReviewPager, handlePtr **tui.OverlayHandle, geo reviewOverlayGeometry) {
	pager.OnClose = func() {
		handle := *handlePtr
		if handle != nil && handle.Hide != nil {
			handle.Hide()
		}
		a.reviewSetTitle("")
	}

	pager.OnCommentRequest = a.makeReviewCommentRequestHandler(handlePtr)
	pager.OnConfirm = a.makeReviewConfirmHandler(handlePtr)
	pager.OnSelectBase = a.makeReviewSelectBaseHandler(pager)
	pager.OnExportReview = a.makeReviewExportHandler(handlePtr, pager)
}

func (a *App) makeReviewCommentRequestHandler(handlePtr **tui.OverlayHandle) func(title, current string, onSubmit func(string)) {
	return func(title, current string, onSubmit func(string)) {
		handle := *handlePtr
		restore := a.reviewReleaseInput(handle)
		if inp := a.subs.getInput(); inp != nil {
			inp.SetText(current)
		}
		a.requestMainInputWithCancel(title, func(text string) {
			onSubmit(text)
			if handle.IsVisible() {
				a.reviewSetTitle(reviewHelpTitle)
				a.reviewCaptureInput(handle)
			}
		}, restore, true)
	}
}

// makeReviewConfirmHandler routes a yes/no confirmation through the main input
// line (same location as comment entry). The user types y/n + Enter; the
// question title is shown on the input separator. This replaces the previous
// inline overlay prompt that required typing the full word "yes" and lived
// inside the pager body (wrong screen region).
func (a *App) makeReviewConfirmHandler(handlePtr **tui.OverlayHandle) func(question string, onResult func(yes bool)) {
	return func(question string, onResult func(yes bool)) {
		handle := *handlePtr
		restore := a.reviewReleaseInput(handle)
		if inp := a.subs.getInput(); inp != nil {
			inp.SetText("")
		}
		a.requestMainInputWithCancel(question+" (y/n)", func(text string) {
			onResult(parseYesNo(text))
			// The action may have closed the overlay (e.g. submit). Only restore
			// capture/title while it is still on screen; otherwise leave focus
			// with the main editor so subsequent commands work.
			if handle.IsVisible() {
				a.reviewSetTitle(reviewHelpTitle)
				a.reviewCaptureInput(handle)
			}
		}, restore, true)
	}
}

// makeReviewExportHandler writes the review Markdown to disk when the user
// presses 'x'. It writes the same content submit sends to the agent
// (Session.MarkdownSummary), but to a timestamped file under the project
// root, without sending to the model and without closing the pager. The
// result is shown on the input separator line, which stays visible while the
// overlay is open; it reverts to the key-binding help on the next
// host-mediated action (comment/base/submit).
func (a *App) makeReviewExportHandler(handlePtr **tui.OverlayHandle, pager *tui.ReviewPager) func() {
	return func() {
		projectDir := a.subs.projectDir
		if projectDir == "" {
			a.reviewSetTitle("Cannot export: project directory unknown")
			return
		}
		path, err := pager.Session.ExportPath(projectDir)
		if err != nil {
			a.reviewSetTitle("Cannot export: " + err.Error())
			return
		}
		if err := pager.Session.Export(pager.Diff, path); err != nil {
			a.reviewSetTitle("Cannot export: " + err.Error())
			return
		}
		a.reviewSetTitle("Exported review to " + filepath.Base(path))
		a.subs.tuiEngine.RequestRender()
	}
}

func (a *App) makeReviewSelectBaseHandler(pager *tui.ReviewPager) func(commits []review.CommitInfo, onSelect func(string)) {
	return func(commits []review.CommitInfo, onSelect func(string)) {
		items := make([]tui.SelectorItem, 0, len(commits))
		for _, c := range commits {
			items = append(items, tui.SelectorItem{
				Value: c.SHA,
				Label: c.SHA[:7] + " " + c.Subject,
			})
		}
		ch := a.subs.tuiEngine.ShowSelector("Select base commit:", items, pager.Session.BaseRef)
		go func() {
			selected := <-ch
			if selected != "" {
				a.apply(func() { onSelect(selected) })
			}
		}()
	}
}

// reviewReleaseInput disables input capture on the review overlay and returns
// a restore func that re-captures input and resets the help title. The caller
// passes the restore func as the request's onCancel so cancellation (Ctrl+C)
// restores the overlay without any App-level "restore" field.
func (a *App) reviewReleaseInput(handle *tui.OverlayHandle) func() {
	if handle != nil && handle.SetCaptureInput != nil {
		handle.SetCaptureInput(false)
	}
	return func() {
		a.reviewSetTitle(reviewHelpTitle)
		a.reviewCaptureInput(handle)
	}
}

func (a *App) reviewCaptureInput(handle *tui.OverlayHandle) {
	if handle != nil && handle.SetCaptureInput != nil {
		handle.SetCaptureInput(true)
	}
}

func (a *App) showPipelineProgress(p *event.PipelineProgress) {
	if a.subs.chat == nil || p == nil || p.Status == "" {
		return
	}
	a.subs.chat.AddSystemMessage(fmt.Sprintf("[pipeline %s] stage %s: %s", p.PipelineID, p.StageID, p.Status))
}

func (a *App) handleModeChangeEvent(e *event.ModeChange) {
	subs := a.subs
	profileName := string(e.NewMode.Major)
	if profileName == "" {
		profileName = string(subs.effectiveModeState().Major)
	}
	subs.statusMsg.Clear()
	subs.footer.SetData(tui.FooterData{
		Workdir:                subs.projectDir,
		Mode:                   string(e.NewMode.Autonomy),
		Profile:                profileName,
		Model:                  activeModelDisplay(subs),
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	})
}

func (a *App) handleInterAgentEvent(e *event.InterAgent) {
	if a.subs.chat == nil {
		return
	}
	if e.From != "" && e.From != "system" && e.From != "user" {
		a.subs.chat.AddAgentMessage(e.From, e.Content)
	} else {
		a.subs.chat.AddSystemMessage(e.Content)
	}
}

func (a *App) handleThinkingLevelChange(e *event.ThinkingLevel) {
	if e == nil {
		return
	}
	a.applyThinkingLevelToUI(e.Level)
	if a.subs.footer == nil {
		return
	}
	data := a.subs.footer.Data()
	data.ThinkingLevel = e.Level
	a.subs.footer.SetData(data)
	a.subs.tuiEngine.RequestRender()
}

func (a *App) handleCompanionCycleChange(e *event.CompanionCycle) {
	if a.subs.footer == nil {
		return
	}
	data := a.subs.footer.Data()
	data.CompanionCycleCount = e.Current
	data.CompanionCycleMax = e.Max
	a.subs.footer.SetData(data)

	a.subs.tuiEngine.RequestRender()
}

func (a *App) handleWorkflowStatusEvent(e *event.WorkflowStatus) {
	if a.subs.footer == nil {
		return
	}
	subs := a.subs
	subs.footer.SetData(tui.FooterData{
		Workdir:                subs.projectDir,
		Mode:                   subs.footer.Data().Mode,
		Profile:                string(subs.effectiveModeState().Major),
		Model:                  activeModelDisplay(subs),
		MinorMode:              subs.footer.Data().MinorMode,
		WorkflowActive:         e.Step < e.TotalSteps,
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	})
}

func (a *App) handleWorkflowProgressEvent(e *event.WorkflowProgress) {
	if a.subs.footer == nil {
		return
	}
	activity := ""
	if e.Status == "running" && e.StageName != "" {
		activity = fmt.Sprintf("stage %d/%d: %s", e.StageIndex+1, e.TotalStages, e.StageName)
	} else if e.Status == "gate" {
		activity = fmt.Sprintf("gate: %s", e.StageName)
	}
	data := a.subs.footer.Data()
	data.WorkflowActive = e.Status == "running" || e.Status == "gate"
	if data.WorkflowActive {
		data.SteeringHint = "type to steer"
	} else {
		data.SteeringHint = ""
	}
	// Clear pending steering indicator; the steering has been consumed
	// when a new progress event fires.
	data.SteeringPending = ""
	if activity != "" {
		data.Activity = activity
	}
	a.subs.footer.SetData(data)
}

func (a *App) handleGoalUpdate(update *event.GoalUpdate) {
	if update == nil || a.subs.chat == nil {
		return
	}

	a.updateGoalFooter(update)

	if update.Change != nil {
		switch update.Change.Kind {
		case goal.GoalChangeLifecycle:
			marker := goaltui.NewMarker((*goal.GoalChange)(update.Change))
			a.subs.chat.AddComponent(marker)
		case goal.GoalChangeCompletion:
			if update.Snapshot != nil {
				a.subs.chat.AddComponent(goaltui.NewCompletion(update.Snapshot))
			}
		}
	}

	if update.Snapshot == nil && a.subs.goalManager != nil {
		a.promoteNextQueuedGoal()
	}
}

func (a *App) updateGoalFooter(update *event.GoalUpdate) {
	if a.subs.goalBubble != nil {
		if update.Snapshot != nil {
			a.subs.goalBubble.SetSnapshot(update.Snapshot)
		} else {
			a.subs.goalBubble.SetSnapshot(nil)
		}
	}
	if a.subs.footer == nil {
		return
	}
	data := a.subs.footer.Data()
	if update.Snapshot == nil {
		data.GoalStatus = ""
		data.GoalObjective = ""
	} else {
		data.GoalStatus = string(update.Snapshot.Status)
		data.GoalObjective = update.Snapshot.Objective
	}
	a.subs.footer.SetData(data)
	if a.subs.tuiEngine != nil {
		a.subs.tuiEngine.RequestRender()
	}
}

func (a *App) promoteNextQueuedGoal() {
	queue, err := a.subs.goalManager.Queue.Read()
	if err != nil || len(queue) == 0 {
		return
	}
	next := queue[0]
	_, removed, err := a.subs.goalManager.Queue.Remove(next.ID)
	if err != nil || removed == nil {
		return
	}
	if _, err := a.subs.goalManager.Mode.CreateGoal(goal.CreateGoalInput{
		Objective: removed.Objective,
	}, goal.GoalActorUser); err != nil {
		_, _ = a.subs.goalManager.Queue.Restore(*removed)
		return
	}
	a.subs.chat.AddSystemMessage(fmt.Sprintf("[goal] auto-promoted queued goal: %s", removed.Objective))
}

// showPanicError displays a rendering panic in the chat and creates an export
// so the error can be investigated. Safe to call from deferred recover().
func (a *App) showPanicError(source string, r any, stack []byte) {
	subs := a.subs

	// Show the error in the chat UI
	if subs.chat != nil {
		// Extract first 3 meaningful stack frames (skip runtime/plugin)
		stackLines := strings.Split(string(stack), "\n")
		var brief []string
		for _, sl := range stackLines {
			if strings.Contains(sl, "/github.com/pijalu/goa/") &&
				!strings.Contains(sl, "_test.go") &&
				len(brief) < 4 {
				brief = append(brief, strings.TrimSpace(sl))
			}
		}
		msg := fmt.Sprintf("⚠️  Internal %s error: %v", source, r)
		if len(brief) > 0 {
			msg += "\n  " + strings.Join(brief, "\n  ")
		}
		subs.chat.AddSystemMessage(msg)
	}

	if subs.tuiEngine != nil {
		subs.tuiEngine.RequestRender()
	}

	// Create an export snapshot for debugging (async, don't block restart)
	go func() {
		issue := fmt.Sprintf("panic: %s error: %v\n\nFull stack:\n%s", source, r, string(stack))
		exportDir := filepath.Join(subs.projectDir, ".goa", "exports")
		_ = os.MkdirAll(exportDir, 0o755)
		outputPath := filepath.Join(exportDir,
			fmt.Sprintf("goa-panic-%s-%s.zip", source, time.Now().Format("20060102-150405")))

		var sessionID string
		if subs.sessionStore != nil {
			sessionID = subs.sessionStore.SessionID()
		}

		ctx := coreContextForCommand(subs, nil)
		cmd := &commands.ExportSessionCommand{
			ProjectDir:  subs.projectDir,
			Issue:       issue,
			OutputPath:  outputPath,
			SessionID:   sessionID,
			IncludeLogs: true,
		}
		if err := cmd.Run(ctx); err != nil {
			log.Printf("[events] failed to create panic export: %v", err)
		}
	}()
}
