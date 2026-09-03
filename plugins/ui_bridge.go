// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"sync"
	"time"
)

// UIPaneDef defines a pane that a plugin wants to add to the TUI.
type UIPaneDef struct {
	ID     string
	Title  string
	Render func() string
}

// UISegmentDef defines a mode line segment a plugin wants to add.
type UISegmentDef struct {
	ID       string
	Priority int
	// Render produces the segment text. ok reports whether a fresh render
	// was produced: false means the render was SKIPPED (e.g. the plugin VM
	// was busy with a parked command/timer frame) and carries no verdict
	// about the segment — consumers must keep the previously rendered text.
	// A skipped render is indistinguishable from data by design: plugins
	// legitimately return empty text to hide a segment (quota's no_api_key),
	// so the skip signal must be separate from the content.
	Render func() (string, bool)
}

// UIDialogDef defines a modal dialog.
type UIDialogDef struct {
	ID     string
	Title  string
	Render func() string
}

// UIBridge provides a JS API for plugins to add UI elements.
// Registered via ExtensionRegistry when plugins are loaded.
//
// The bridge is safe for concurrent use: plugins mutate it from the plugin
// runner while the TUI reads rendered segments from the render loop.
type UIBridge struct {
	mu       sync.RWMutex
	panes    []UIPaneDef
	segments []UISegmentDef
	modals   []UIDialogDef
	// refresh broadcasts segment re-render requests (goa.ui.refreshSegment)
	// to the TUI. Buffered so a plugin never blocks the runner on a render.
	refresh chan string

	// Confirm state (plan §4). confirmConsumer reports that a live TUI drain
	// owns queued confirms (false ⇒ RequestConfirm fails closed immediately).
	// pendingConfirms holds requested-but-unanswered jobs keyed by monotonic
	// ID; membership is the exactly-once guard for delivery.
	// confirmQueue preserves FIFO order of jobs not yet picked up; cap-1
	// confirmNotify coalesces wake-ups because the drain re-checks the queue
	// after every signal.
	confirmConsumer bool
	nextConfirmID   int64
	pendingConfirms map[int64]ConfirmJob
	confirmQueue    []ConfirmJob
	confirmNotify   chan struct{}
	// blockGuard, when installed by the host, reports that the CURRENT
	// goroutine must never block (it is the TUI command loop). goa.ui.confirm
	// checks it and fails closed instead of deadlocking the UI thread.
	blockGuard func() bool
}

// NewUIBridge creates a new UI bridge.
func NewUIBridge() *UIBridge {
	return &UIBridge{refresh: make(chan string, 16), confirmNotify: make(chan struct{}, 1)}
}

// AddPane registers a plugin pane.
func (b *UIBridge) AddPane(def UIPaneDef) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.panes = append(b.panes, def)
}

// AddSegment registers a plugin mode line segment.
func (b *UIBridge) AddSegment(def UISegmentDef) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.segments = append(b.segments, def)
}

// AddModal registers a plugin modal dialog.
func (b *UIBridge) AddModal(def UIDialogDef) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.modals = append(b.modals, def)
}

// Panes returns all registered plugin panes.
func (b *UIBridge) Panes() []UIPaneDef {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]UIPaneDef, len(b.panes))
	copy(out, b.panes)
	return out
}

// Segments returns all registered plugin segments.
func (b *UIBridge) Segments() []UISegmentDef {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]UISegmentDef, len(b.segments))
	copy(out, b.segments)
	return out
}

// Modals returns all registered plugin modals.
func (b *UIBridge) Modals() []UIDialogDef {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]UIDialogDef, len(b.modals))
	copy(out, b.modals)
	return out
}

// RequestRefresh signals that a segment's rendered content changed. The
// notification is non-blocking; a saturated channel drops the oldest intent
// (the TUI re-renders the latest state anyway, so coalescing is safe).
func (b *UIBridge) RequestRefresh(segmentID string) {
	select {
	case b.refresh <- segmentID:
	default:
	}
}

// RefreshRequests returns the channel the TUI drains to learn about segment
// updates. May return nil-receiver-safe values; callers range/select on it.
func (b *UIBridge) RefreshRequests() <-chan string {
	return b.refresh
}

// ── Confirm API (plugins plan §4, Phase M3) ──
//
// goa.ui.confirm is a channel-based request/response flow: a plugin asks the
// user a multiple-choice question; the TUI renders a modal selection card and
// the answer flows back over a reply channel. Requests serialize FIFO — one
// modal is visible at a time.
//
// Deadlock invariant (plan §4): while a confirm is pending, nothing calls
// INTO the plugin VM from the UI side. The card reads only plain data copied
// out of the request; resolution happens from key handling on the TUI loop
// and never re-enters JS.

const (
	// ConfirmStyleDefault renders an option without special coloring.
	ConfirmStyleDefault = "default"
	// ConfirmStyleOK marks a safe/affirmative choice (theme success color).
	ConfirmStyleOK = "ok"
	// ConfirmStyleDanger marks a destructive/irreversible choice (theme
	// critical color).
	ConfirmStyleDanger = "danger"

	// MaxConfirmTimeout caps every confirm wait. Timeout 0 means "no explicit
	// timeout" but still expires at this cap so a plugin can never wedge its
	// own runner (and the global VM lock rotation) forever on an unanswered
	// modal.
	MaxConfirmTimeout = 5 * time.Minute

	// ErrConfirmNoUI is the ConfirmResponse.Err value used when no TUI
	// consumer is attached (headless runs, tests, `-e` script mode). The
	// response is fail-closed: Cancelled=true so callers must treat it as
	// "the user declined", never as consent.
	ErrConfirmNoUI = "no-ui"

	// ErrConfirmTimeout is ConfirmResponse.Err when the wait exceeded the
	// request's Timeout (or MaxConfirmTimeout for 0).
	ErrConfirmTimeout = "timeout"
)

// ConfirmOption is one selectable row of a confirm dialog.
type ConfirmOption struct {
	ID    string
	Label string
	// Style is "ok" | "danger" | "default" (see ConfirmStyle* constants).
	// Unknown values render as default.
	Style string
}

// ConfirmRequest describes a confirmation question shown to the user.
type ConfirmRequest struct {
	// PluginID identifies the asking plugin (stamped by the JS bridge from
	// the manifest; direct Go callers may leave it empty).
	PluginID string
	Title    string
	Body     string
	// Options must contain at least one entry. When AllowCancel is true the
	// TUI appends an implicit Cancel row — plugins list only real choices.
	Options []ConfirmOption
	// DefaultID is the initially highlighted option ID. Unknown or empty
	// values select the first option.
	DefaultID string
	// AllowCancel lets the user dismiss the dialog without choosing
	// (Esc / Ctrl+C / implicit Cancel row). False forces a real choice.
	AllowCancel bool
	// Timeout bounds the wait. 0 means no explicit timeout, capped at
	// MaxConfirmTimeout; positive values are clamped to the same cap.
	Timeout time.Duration
}

// ConfirmResponse is the answer delivered over the reply channel.
type ConfirmResponse struct {
	// ID is the chosen option ID ("", meaningless, when Cancelled).
	ID string
	// Cancelled reports dismissal: Esc, the implicit Cancel row, timeout,
	// shutdown, or the no-UI fail-closed path.
	Cancelled bool
	// Err distinguishes abnormal endings ("no-ui", "timeout", "shutdown");
	// empty for plain user cancellation and normal choices.
	Err string
}

// ConfirmJob is a queued confirmation handed to the UI consumer. The consumer
// renders Req and reports the outcome through Resolve exactly once; after
// Resolve (or a bridge-side timeout/cancel) the job is inert — further
// resolves are dropped because the buffered reply channel already holds the
// single answer.
type ConfirmJob struct {
	ID  int64
	Req ConfirmRequest

	bridge *UIBridge
	reply  chan ConfirmResponse
	done   chan struct{}
}

// Resolve delivers the user's answer to the waiting caller. Safe to call from
// any goroutine; a second resolve for the same job is a no-op.
func (j ConfirmJob) Resolve(resp ConfirmResponse) {
	if j.bridge != nil {
		j.bridge.finishConfirm(j.ID, resp)
		return
	}
	// Detached job (bridge discarded): best-effort delivery, no lifecycle
	// bookkeeping left to do.
	deliverConfirm(j.reply, resp)
}

// Done returns a channel closed when the job leaves the pending state for ANY
// reason (user answer, timeout, CancelAll). The UI presenter watches it to
// hide a still-visible card whose wait already ended out-of-band (e.g. the
// 5-minute cap fired while nobody answered). Closed exactly once by the
// pending-map winner; never closed for detached jobs (no presenter exists).
func (j ConfirmJob) Done() <-chan struct{} { return j.done }

// SetConfirmConsumer attaches or detaches the TUI drain. Called by the app
// when the plugin UI activates (TUI exists) and on teardown.
func (b *UIBridge) SetConfirmConsumer(enabled bool) {
	b.mu.Lock()
	b.confirmConsumer = enabled
	b.mu.Unlock()
}

// SetBlockGuard installs the host's "this goroutine must not block" detector
// (the TUI command loop). The goa.ui.confirm binding consults it before
// waiting: a confirm requested from the UI thread could never be answered
// (the overlay itself needs that thread to render and read keys), so it fails
// closed immediately with {cancelled:true, error:"ui-thread"} instead of
// deadlocking until the timeout cap.
func (b *UIBridge) SetBlockGuard(fn func() bool) {
	b.mu.Lock()
	b.blockGuard = fn
	b.mu.Unlock()
}

// onForbiddenThread reports whether the current goroutine must not block on a
// confirm. No guard installed (headless, tests, plugin harness) ⇒ never
// forbidden.
func (b *UIBridge) onForbiddenThread() bool {
	b.mu.RLock()
	fn := b.blockGuard
	b.mu.RUnlock()
	return fn != nil && fn()
}

// RequestConfirm queues a confirmation and returns the reply channel
// (buffered size 1: resolution never blocks the resolver). Validation
// failures and missing consumers are answered IMMEDIATELY — the returned
// channel is ready to read before RequestConfirm returns in those cases.
func (b *UIBridge) RequestConfirm(req ConfirmRequest) <-chan ConfirmResponse {
	reply := make(chan ConfirmResponse, 1)
	if errStr := validateConfirmRequest(req); errStr != "" {
		reply <- ConfirmResponse{Err: errStr}
		return reply
	}
	req = normalizeConfirmRequest(req)

	b.mu.Lock()
	if !b.confirmConsumer {
		b.mu.Unlock()
		reply <- ConfirmResponse{Cancelled: true, Err: ErrConfirmNoUI}
		return reply
	}
	b.nextConfirmID++
	job := ConfirmJob{
		ID: b.nextConfirmID, Req: req, bridge: b,
		reply: reply, done: make(chan struct{}),
	}
	if b.pendingConfirms == nil {
		b.pendingConfirms = make(map[int64]ConfirmJob)
	}
	b.pendingConfirms[job.ID] = job
	b.confirmQueue = append(b.confirmQueue, job)
	b.mu.Unlock()

	b.signalConfirm()

	// Enforce the wait cap bridge-side so it applies to every consumer,
	// presented or still queued. finishConfirm is idempotent (pending-map
	// guard), so a late user answer after expiry is dropped cleanly.
	time.AfterFunc(req.Timeout, func() {
		b.finishConfirm(job.ID, ConfirmResponse{Cancelled: true, Err: ErrConfirmTimeout})
	})
	return reply
}

// validateConfirmRequest returns a descriptive error string ("" = valid).
// Mirrors the JS-layer validation; kept defensive because RequestConfirm is a
// public Go API.
func validateConfirmRequest(req ConfirmRequest) string {
	if len(req.Options) == 0 {
		return "confirm requires at least one option"
	}
	for _, opt := range req.Options {
		if opt.ID == "" {
			return "confirm option id must not be empty"
		}
		if opt.Label == "" {
			return "confirm option label must not be empty"
		}
	}
	return ""
}

// normalizeConfirmRequest canonicalizes styles and clamps the timeout.
func normalizeConfirmRequest(req ConfirmRequest) ConfirmRequest {
	out := req
	out.Options = make([]ConfirmOption, len(req.Options))
	copy(out.Options, req.Options)
	for i, opt := range out.Options {
		switch opt.Style {
		case ConfirmStyleOK, ConfirmStyleDanger:
			// keep semantic style
		default:
			out.Options[i].Style = ConfirmStyleDefault
		}
	}
	if out.Timeout <= 0 || out.Timeout > MaxConfirmTimeout {
		out.Timeout = MaxConfirmTimeout
	}
	return out
}

// signalConfirm wakes the drain goroutine without ever blocking a plugin.
func (b *UIBridge) signalConfirm() {
	select {
	case b.confirmNotify <- struct{}{}:
	default:
	}
}

// ConfirmRequests returns the coalesced wake-up channel the UI drain selects
// on. A receive means "check NextConfirm until it returns false" — signals
// coalesce, so the queue is authoritative, not the signal count.
func (b *UIBridge) ConfirmRequests() <-chan struct{} {
	return b.confirmNotify
}

// NextConfirm pops the oldest queued job (FIFO). ok=false when the queue is
// empty. A popped job stays in pendingConfirms until resolved or expired, so
// timeouts still fire for presented dialogs.
func (b *UIBridge) NextConfirm() (ConfirmJob, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.confirmQueue) == 0 {
		return ConfirmJob{}, false
	}
	job := b.confirmQueue[0]
	b.confirmQueue[0] = ConfirmJob{} // drop reference for GC hygiene
	b.confirmQueue = b.confirmQueue[1:]
	return job, true
}

// PendingConfirmCount reports how many requests are unanswered (queued plus
// presented). Diagnostic/test helper.
func (b *UIBridge) PendingConfirmCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.pendingConfirms)
}

// finishConfirm resolves the job identified by id exactly once: the winner of
// the pending-map deletion delivers the reply and closes the lifecycle.
// Subsequent callers (late user input vs timeout race) find no entry and
// return silently.
func (b *UIBridge) finishConfirm(id int64, resp ConfirmResponse) {
	b.mu.Lock()
	job, ok := b.pendingConfirms[id]
	if ok {
		delete(b.pendingConfirms, id)
		for i, q := range b.confirmQueue {
			if q.ID == id {
				b.confirmQueue = append(b.confirmQueue[:i], b.confirmQueue[i+1:]...)
				break
			}
		}
	}
	b.mu.Unlock()
	if !ok {
		return
	}
	deliverConfirm(job.reply, resp)
	// Broadcast completion AFTER the answer lands so a presenter watching
	// Done() observes a finished job (hide the card, promote the next).
	if job.done != nil {
		close(job.done)
	}
}

// deliverConfirm sends on the buffered reply channel without blocking; the
// buffer guarantees the first (and only) answer lands even if nobody reads.
func deliverConfirm(reply chan<- ConfirmResponse, resp ConfirmResponse) {
	if reply == nil {
		return
	}
	select {
	case reply <- resp:
	default:
	}
}

// CancelAllConfirms fails every outstanding request (queued and presented)
// with {Cancelled:true, Err: reason}. Used on TUI teardown so no plugin stays
// parked on a dead UI.
func (b *UIBridge) CancelAllConfirms(reason string) {
	b.mu.Lock()
	jobs := make([]ConfirmJob, 0, len(b.pendingConfirms))
	for _, j := range b.pendingConfirms {
		jobs = append(jobs, j)
	}
	b.pendingConfirms = make(map[int64]ConfirmJob)
	b.confirmQueue = nil
	b.mu.Unlock()
	for _, j := range jobs {
		deliverConfirm(j.reply, ConfirmResponse{Cancelled: true, Err: reason})
	}
}
