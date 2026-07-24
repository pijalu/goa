// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"context"
	"sync"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/perms"
)

// ExecutionController manages execution mode state machine, confirmation
// flow, and review queue. Full implementation is in M08 — this file defines
// the interface consumed by M02 commands (/mode, /stop, /retry, /undo) and
// the stub implementation for early integration testing.
type ExecutionController struct {
	mode            internal.ExecutionMode
	confirmQueue    chan internal.ConfirmRequest
	reviewQueue     []internal.ReviewItem
	sessionState    *SessionState
	permissionRules []perms.Rule
	permissionMu    sync.RWMutex

	// pending holds an in-flight confirmation request when a consumer has
	// been registered via SetConfirmConsumer. It is used to deliver a
	// response reliably and to cancel the request when the consumer is
	// removed or the controller stops.
	pendingMu sync.Mutex
	pending   *pendingConfirm

	// consumer receives confirmation requests when set. If nil,
	// RequestConfirm falls back to the ConfirmNo safe default.
	consumer ConfirmConsumer
}

// ConfirmConsumer receives a confirmation request and reports the user's
// decision by sending exactly one value on req.ResponseChan. The first
// response wins; returning without a response, or returning an error, is
// treated as the ConfirmNo safe default. The consumer must not receive from
// req.ResponseChan: the controller is its only reader.
type ConfirmConsumer func(ctx context.Context, req internal.ConfirmRequest) error

// pendingConfirm tracks a request and a cancellation function so the
// controller can abort a blocked confirmation when the consumer changes.
type pendingConfirm struct {
	req    internal.ConfirmRequest
	cancel context.CancelFunc
	done   chan struct{}
}

// NewExecutionController creates a new execution controller.
// If sessionState is non-nil, ShouldConfirm reads autonomy from there;
// otherwise it falls back to the old ExecutionMode.
func NewExecutionController(cfg *config.Config, sessionState *SessionState) *ExecutionController {
	ec := &ExecutionController{
		confirmQueue: make(chan internal.ConfirmRequest, 10),
		reviewQueue:  make([]internal.ReviewItem, 0),
		sessionState: sessionState,
	}
	if cfg != nil {
		ec.mode = cfg.Execution.Mode
		ec.permissionRules = cfg.Permissions
	}
	return ec
}

// PermissionRules returns the current permission rules.
func (ec *ExecutionController) PermissionRules() []perms.Rule {
	ec.permissionMu.RLock()
	defer ec.permissionMu.RUnlock()
	out := make([]perms.Rule, len(ec.permissionRules))
	copy(out, ec.permissionRules)
	return out
}

// SetPermissionRules replaces the current permission rules.
func (ec *ExecutionController) SetPermissionRules(rules []perms.Rule) {
	ec.permissionMu.Lock()
	defer ec.permissionMu.Unlock()
	if rules == nil {
		ec.permissionRules = nil
		return
	}
	ec.permissionRules = make([]perms.Rule, len(rules))
	copy(ec.permissionRules, rules)
}

// Mode returns the current execution mode.
func (ec *ExecutionController) Mode() internal.ExecutionMode {
	return ec.mode
}

// SetMode changes the execution mode.
func (ec *ExecutionController) SetMode(mode internal.ExecutionMode) {
	ec.mode = mode
}

// Autonomy returns the current autonomy level from SessionState.
func (ec *ExecutionController) Autonomy() internal.AutonomyLevel {
	if ec.sessionState != nil {
		return ec.sessionState.Current().Autonomy
	}
	// Fall back to old ExecutionMode conversion
	return autonomyFromMode(ec.mode)
}

// autonomyFromMode converts old ExecutionMode to AutonomyLevel.
func autonomyFromMode(mode internal.ExecutionMode) internal.AutonomyLevel {
	switch mode {
	case internal.ExecutionYolo:
		return internal.AutonomyYolo
	case internal.ExecutionSolo:
		return internal.AutonomySolo
	case internal.ExecutionConfirm:
		return internal.AutonomyConfirm
	case internal.ExecutionReview:
		return internal.AutonomyReview
	default:
		return internal.AutonomySolo
	}
}

// ShouldConfirm checks if a tool call should be confirmed based on the
// current autonomy level, tool name, and permission rules.
func (ec *ExecutionController) ShouldConfirm(toolName, input string) bool {
	if decision := ec.evaluatePermissionRules(toolName); decision != "" {
		return decision == perms.DecisionAsk
	}

	switch ec.Autonomy() {
	case internal.AutonomyYolo, internal.AutonomySolo:
		return false
	case internal.AutonomyConfirm:
		return true
	case internal.AutonomyReview:
		return toolName == "edit" || toolName == "write"
	default:
		return false
	}
}

func (ec *ExecutionController) evaluatePermissionRules(toolName string) perms.Decision {
	ec.permissionMu.RLock()
	rules := ec.permissionRules
	ec.permissionMu.RUnlock()
	if len(rules) == 0 {
		return ""
	}
	engine := perms.NewEngine(rules)
	res := engine.Evaluate(toolName, "")
	if !res.Matched {
		return ""
	}
	return res.Decision
}

// ConfirmQueue returns the channel used to deliver confirmation requests.
//
// Deprecated: SetConfirmConsumer is the preferred API. ConfirmQueue is kept
// for existing TUI consumers that read from the channel directly.
func (ec *ExecutionController) ConfirmQueue() <-chan internal.ConfirmRequest {
	return ec.confirmQueue
}

// RunConfirmConsumer installs a consumer that reads from the legacy
// confirmQueue. Call once at subsystem startup if the TUI uses the queue.
// The consumer runs until StopConfirmConsumer is called or the controller
// is garbage-collected.
func (ec *ExecutionController) RunConfirmConsumer(ctx context.Context, consumer ConfirmConsumer) {
	ec.SetConfirmConsumer(func(c context.Context, req internal.ConfirmRequest) error {
		// Bridge the legacy queue-based API to the callback API.
		select {
		case ec.confirmQueue <- req:
		case <-c.Done():
			return c.Err()
		case <-ctx.Done():
			return ctx.Err()
		}

		// Wait for a response on the legacy queue or cancellation.
		select {
		case <-c.Done():
			return c.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	})
}

// StopConfirmConsumer removes the active consumer and cancels any pending
// confirmation.
func (ec *ExecutionController) StopConfirmConsumer() {
	ec.SetConfirmConsumer(nil)
}

// SetConfirmConsumer installs a function that will receive confirmation
// requests from RequestConfirm. Setting a consumer also clears the legacy
// confirmQueue. Passing nil removes the consumer and causes any pending
// confirmation to be cancelled with ConfirmNo.
func (ec *ExecutionController) SetConfirmConsumer(consumer ConfirmConsumer) {
	ec.pendingMu.Lock()
	defer ec.pendingMu.Unlock()

	ec.consumer = consumer
	if p := ec.pending; p != nil {
		ec.pending = nil
		if p.cancel != nil {
			p.cancel()
		}
	}
}

// RequestConfirm blocks until the user responds to a confirmation prompt.
// If no consumer is installed, the queue is full, or the consumer returns
// an error, it returns ConfirmNo (safe default). When autonomy is yolo it
// returns ConfirmYes immediately without consulting the consumer.
// Permission rules override autonomy: deny -> ConfirmNo, allow -> ConfirmYes.
func (ec *ExecutionController) RequestConfirm(toolName, input string) internal.ConfirmResponse {
	if decision := ec.evaluatePermissionRules(toolName); decision != "" {
		switch decision {
		case perms.DecisionDeny:
			return internal.ConfirmNo
		case perms.DecisionAllow:
			return internal.ConfirmYes
		}
	}
	if !ec.ShouldConfirm(toolName, input) {
		return internal.ConfirmYes
	}

	return ec.waitForConsumerResponse(toolName, input)
}

// confirmSlot holds the outcome of a single confirmation request. The first
// value resolved wins; later resolutions are discarded. This makes the result
// immune to duplicate or racy sends: previously a "safety net" send could
// land in the response channel after the real response had been consumed by
// the wrong party, flipping a ConfirmYes into a ConfirmNo.
type confirmSlot struct {
	mu  sync.Mutex
	ch  chan internal.ConfirmResponse // buffered (cap 1); closed on resolve
	set bool
}

func newConfirmSlot() *confirmSlot {
	return &confirmSlot{ch: make(chan internal.ConfirmResponse, 1)}
}

// resolve records resp as the outcome unless an outcome was already recorded.
func (s *confirmSlot) resolve(resp internal.ConfirmResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.set {
		return
	}
	s.set = true
	s.ch <- resp
	close(s.ch)
}

// outcome returns a channel that yields the recorded response once resolved.
func (s *confirmSlot) outcome() <-chan internal.ConfirmResponse {
	return s.ch
}

func (ec *ExecutionController) waitForConsumerResponse(toolName, input string) internal.ConfirmResponse {
	slot := newConfirmSlot()
	req := internal.ConfirmRequest{
		ToolName:     toolName,
		ToolInput:    input,
		ResponseChan: make(chan internal.ConfirmResponse, 1),
	}

	ec.pendingMu.Lock()
	consumer := ec.consumer
	if consumer == nil {
		ec.pendingMu.Unlock()
		return internal.ConfirmNo
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	ec.pending = &pendingConfirm{req: req, cancel: cancel, done: done}
	ec.pendingMu.Unlock()
	defer func() {
		ec.pendingMu.Lock()
		if ec.pending != nil && ec.pending.req == req {
			ec.pending = nil
		}
		ec.pendingMu.Unlock()
		close(done)
		cancel()
	}()

	// The router is the sole reader of ResponseChan: it routes the first
	// response into the slot and applies the ConfirmNo safe default when the
	// consumer finishes without responding or the request is cancelled.
	// Because it is the only reader, no handshake or scheduling is needed —
	// the response can never be consumed by anyone else.
	consumerDone := make(chan struct{})
	go forwardConfirmResponses(req.ResponseChan, slot, consumerDone, ctx.Done())
	go func() {
		defer close(consumerDone)
		_ = consumer(ctx, req)
	}()

	return readConfirmResponse(slot, ctx.Done())
}

// forwardConfirmResponses routes the first response sent on respCh into the
// slot. If the consumer finishes without a routed response, it drains a
// response the consumer may have sent just before returning; otherwise it
// applies the ConfirmNo safe default. Every exit path resolves the slot, so
// readConfirmResponse can never block indefinitely.
func forwardConfirmResponses(respCh <-chan internal.ConfirmResponse, slot *confirmSlot, consumerDone, ctxDone <-chan struct{}) {
	select {
	case resp := <-respCh:
		slot.resolve(resp)
	case <-consumerDone:
		// A response sent just before the consumer returned is already
		// buffered: closing consumerDone happens-after it in program order,
		// so this single non-blocking drain observes it reliably.
		select {
		case resp := <-respCh:
			slot.resolve(resp)
		default:
			slot.resolve(internal.ConfirmNo)
		}
	case <-ctxDone:
		slot.resolve(internal.ConfirmNo)
	}
}

// readConfirmResponse waits for the slot to be resolved or the request to be
// cancelled. forwardConfirmResponses guarantees the slot is resolved on every
// path, so the slot case always wins once an outcome exists.
func readConfirmResponse(slot *confirmSlot, ctxDone <-chan struct{}) internal.ConfirmResponse {
	select {
	case resp := <-slot.outcome():
		return resp
	case <-ctxDone:
		return internal.ConfirmNo
	}
}

// QueueEdit adds a tool call to the review queue.
func (ec *ExecutionController) QueueEdit(toolName, filePath string) *internal.ReviewItem {
	item := &internal.ReviewItem{
		TurnID:   len(ec.reviewQueue) + 1,
		ToolName: toolName,
		FilePath: filePath,
		Diff:     "",
		Approved: nil,
	}
	ec.reviewQueue = append(ec.reviewQueue, *item)
	return item
}

// ShowReviewQueue returns all pending review items.
func (ec *ExecutionController) ShowReviewQueue() []internal.ReviewItem {
	return ec.reviewQueue
}

// ApplyReviewQueue processes approvals and returns approved items.
func (ec *ExecutionController) ApplyReviewQueue(approvals map[int]bool) error {
	for i := range ec.reviewQueue {
		if approved, ok := approvals[i]; ok {
			ec.reviewQueue[i].Approved = &approved
		}
	}
	return nil
}
