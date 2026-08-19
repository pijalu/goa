// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"path/filepath"

	"github.com/pijalu/goa/core/plan"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/internal/review"
	"github.com/pijalu/goa/tui"
)

const planPagerHelpTitle = "q:close n/p:item c:comment s:submit a:approve"
const planStatusHelpTitle = "q:close ↑↓:select enter:detail"

// showPlanPager opens the plan-annotation pager as an overlay. The pager was
// constructed by the /plan review command, which wired store-close into
// OnClose and annotation submission/approval callbacks; this method layers on
// the overlay chrome (close hides the overlay, comment/confirm modals,
// approve also closes the overlay since execution then starts).
func (a *App) showPlanPager(m *event.ShowPlanPager) {
	if a.subs.tuiEngine == nil || m == nil || m.Pager == nil {
		return
	}
	pager, ok := m.Pager.(*tui.PlanPager)
	if !ok {
		return
	}
	pager.RequestRender = func() {
		a.subs.tuiEngine.RequestRender()
	}
	geo := reviewOverlayGeometryFor(terminalRowsOr(a.subs.tuiEngine.TerminalRows(), 24))
	pager.SetViewport(terminalColsOr(a.subs.tuiEngine.TerminalCols(), 80), geo.height)

	var handle *tui.OverlayHandle
	a.wirePlanPagerClose(pager, &handle)
	a.wirePlanPagerApprove(pager)

	pager.OnCommentRequest = a.makeCommentRequestHandler(&handle, planPagerHelpTitle)
	pager.OnConfirm = a.makeConfirmHandler(&handle, planPagerHelpTitle)

	handle = a.subs.tuiEngine.ShowOverlay(pager, tui.OverlayOptions{
		Width:        geo.width,
		Height:       geo.height,
		BottomOffset: geo.bottomOffset,
		CaptureInput: true,
	})
	a.reviewSetTitle(planPagerHelpTitle)
}

// terminalColsOr/terminalRowsOr substitute sane defaults when the engine
// reports a non-positive size (e.g. detached/headless tests).
func terminalColsOr(w, fallback int) int {
	if w <= 0 {
		return fallback
	}
	return w
}

func terminalRowsOr(h, fallback int) int {
	if h <= 0 {
		return fallback
	}
	return h
}

// wirePlanPagerClose chains the command's OnClose (closes the plan store)
// with overlay teardown. The pager's close() calls OnClose when the user
// presses q.
func (a *App) wirePlanPagerClose(pager *tui.PlanPager, handle **tui.OverlayHandle) {
	cmdOnClose := pager.OnClose
	pager.OnClose = func() {
		if cmdOnClose != nil {
			cmdOnClose()
		}
		if h := *handle; h != nil && h.Hide != nil {
			h.Hide()
		}
		a.reviewSetTitle("")
	}
}

// wirePlanPagerApprove closes the overlay through the same path as 'q' after
// a successful approve (execution starts; the pager's review job is done). A
// failed approve (e.g. already approved) keeps the pager open for inspection.
func (a *App) wirePlanPagerApprove(pager *tui.PlanPager) {
	cmdOnApprove := pager.OnApprovePlan
	pager.OnApprovePlan = func() {
		if cmdOnApprove != nil {
			cmdOnApprove()
		}
		if p := pager.Store.Plan(); p == nil || p.Status != plan.PlanApproved && p.Status != plan.PlanExecuting {
			return
		}
		if pagerOnClose := pager.OnClose; pagerOnClose != nil {
			pagerOnClose()
		}
	}
}

// showPlanStatus opens the read-only plan-status overlay. The store was
// opened by the /plan status command and is closed here when the overlay is
// dismissed. Returns the overlay component for tests; may be nil.
func (a *App) showPlanStatus(m *event.ShowPlanStatus) *tui.PlanStatusOverlay {
	if a.subs.tuiEngine == nil || m == nil || m.Store == nil {
		return nil
	}
	store, ok := m.Store.(*plan.Store)
	if !ok {
		return nil
	}
	overlay := tui.NewPlanStatusOverlay(store)
	overlay.RequestRender = func() {
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
	overlay.SetViewport(w, geo.height)

	var handle *tui.OverlayHandle
	overlay.OnClose = func() {
		_ = store.Close()
		if handle != nil && handle.Hide != nil {
			handle.Hide()
		}
		a.reviewSetTitle("")
	}

	handle = a.subs.tuiEngine.ShowOverlay(overlay, tui.OverlayOptions{
		Width:        geo.width,
		Height:       geo.height,
		BottomOffset: geo.bottomOffset,
		CaptureInput: true,
	})
	a.reviewSetTitle(planStatusHelpTitle)
	return overlay
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
	return a.makeCommentRequestHandler(handlePtr, reviewHelpTitle)
}

// makeCommentRequestHandler routes a free-form text entry (e.g. pager
// comment) through the main input line, restoring overlay input capture and
// helpTitle when the entry completes.
func (a *App) makeCommentRequestHandler(handlePtr **tui.OverlayHandle, helpTitle string) func(title, current string, onSubmit func(string)) {
	return func(title, current string, onSubmit func(string)) {
		handle := *handlePtr
		restore := a.reviewReleaseInput(handle)
		if inp := a.subs.getInput(); inp != nil {
			inp.SetText(current)
		}
		a.requestMainInputWithCancel(title, func(text string) {
			onSubmit(text)
			if handle.IsVisible() {
				a.reviewSetTitle(helpTitle)
				a.reviewCaptureInput(handle)
			}
		}, restore)
	}
}

// makeReviewConfirmHandler routes a yes/no confirmation through the main input
// line (same location as comment entry). The user types y/n + Enter; the
// question title is shown on the input separator. This replaces the previous
// inline overlay prompt that required typing the full word "yes" and lived
// inside the pager body (wrong screen region).
func (a *App) makeReviewConfirmHandler(handlePtr **tui.OverlayHandle) func(question string, onResult func(yes bool)) {
	return a.makeConfirmHandler(handlePtr, reviewHelpTitle)
}

// makeConfirmHandler routes a yes/no confirmation through the main input
// line (same location as comment entry). The user types y/n + Enter; the
// question title is shown on the input separator. helpTitle is restored when
// the overlay is still visible after the answer.
func (a *App) makeConfirmHandler(handlePtr **tui.OverlayHandle, helpTitle string) func(question string, onResult func(yes bool)) {
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
				a.reviewSetTitle(helpTitle)
				a.reviewCaptureInput(handle)
			}
		}, restore)
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
		if err := pager.Session.Export(path); err != nil {
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
