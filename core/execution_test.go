// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
)

// executionTestConfig creates a minimal config for execution tests.
func executionTestConfig(mode internal.ExecutionMode) *config.Config {
	return &config.Config{
		Execution: config.ExecutionConfig{Mode: mode},
	}
}

func newEC(mode internal.ExecutionMode) *ExecutionController {
	return NewExecutionController(executionTestConfig(mode), nil)
}

// TestExecutionModeYolo verifies yolo mode never confirms.
func TestExecutionModeYolo(t *testing.T) {
	ec := newEC(internal.ExecutionYolo)
	if ec.ShouldConfirm("read", `{"path":"x"}`) {
		t.Error("yolo mode should not confirm any tool")
	}
}

// TestExecutionModeConfirm verifies confirm mode confirms all tools.
func TestExecutionModeConfirm(t *testing.T) {
	ec := newEC(internal.ExecutionConfirm)
	if !ec.ShouldConfirm("read", `{"path":"x"}`) {
		t.Error("confirm mode should confirm all tools")
	}
	if !ec.ShouldConfirm("bash", "ls") {
		t.Error("confirm mode should confirm bash")
	}
}

// TestExecutionModeSolo verifies solo mode behaves like yolo (auto-execute).
func TestExecutionModeSolo(t *testing.T) {
	ec := newEC(internal.ExecutionSolo)
	if ec.ShouldConfirm("read", `{"path":"x"}`) {
		t.Error("solo mode should not confirm reads")
	}
	if ec.ShouldConfirm("edit", `{"path":"x","operation":"replace"}`) {
		t.Error("solo mode should not confirm edits")
	}
}

// TestExecutionModeReview verifies review mode only confirms edits.
func TestExecutionModeReview(t *testing.T) {
	ec := newEC(internal.ExecutionReview)
	if ec.ShouldConfirm("read", `{"path":"x"}`) {
		t.Error("review mode should not confirm reads")
	}
	if !ec.ShouldConfirm("edit", `{"path":"x","operation":"replace"}`) {
		t.Error("review mode should confirm edit")
	}
	if !ec.ShouldConfirm("write", `{"path":"x","content":"y"}`) {
		t.Error("review mode should confirm write")
	}
}

// TestExecutionSetMode verifies mode switching.
func TestExecutionSetMode(t *testing.T) {
	ec := newEC(internal.ExecutionYolo)
	if ec.Mode() != internal.ExecutionYolo {
		t.Errorf("Mode = %q, want %q", ec.Mode(), internal.ExecutionYolo)
	}
	ec.SetMode(internal.ExecutionConfirm)
	if ec.Mode() != internal.ExecutionConfirm {
		t.Errorf("Mode = %q, want %q", ec.Mode(), internal.ExecutionConfirm)
	}
}

// TestExecutionReviewQueue verifies review queue operations.
func TestExecutionReviewQueue(t *testing.T) {
	ec := newEC(internal.ExecutionReview)

	item := ec.QueueEdit("edit", "src/main.go")
	if item == nil {
		t.Fatal("QueueEdit returned nil")
	}
	if item.FilePath != "src/main.go" {
		t.Errorf("FilePath = %q, want %q", item.FilePath, "src/main.go")
	}
	if item.Approved != nil {
		t.Error("New ReviewItem should have nil Approved")
	}

	queue := ec.ShowReviewQueue()
	if len(queue) != 1 {
		t.Errorf("ShowReviewQueue = %d, want 1", len(queue))
	}
}

// TestExecutionReviewQueueApply verifies review queue approval.
func TestExecutionReviewQueueApply(t *testing.T) {
	ec := newEC(internal.ExecutionReview)
	ec.QueueEdit("edit", "a.go")
	ec.QueueEdit("edit", "b.go")

	approvals := map[int]bool{0: true, 1: false}
	if err := ec.ApplyReviewQueue(approvals); err != nil {
		t.Fatalf("ApplyReviewQueue: %v", err)
	}

	queue := ec.ShowReviewQueue()
	if len(queue) != 2 {
		t.Fatalf("ShowReviewQueue = %d, want 2", len(queue))
	}
	if *queue[0].Approved != true {
		t.Error("Item 0 should be approved")
	}
	if *queue[1].Approved != false {
		t.Error("Item 1 should be rejected")
	}
}

// TestExecutionRequestConfirm_NoConsumerReturnsNo verifies the safe default
// when no consumer is installed.
func TestExecutionRequestConfirm_NoConsumerReturnsNo(t *testing.T) {
	ec := newEC(internal.ExecutionConfirm)

	// Without a consumer, RequestConfirm must reject safely.
	resp := ec.RequestConfirm("read", `{"path":"x"}`)
	if resp != internal.ConfirmNo {
		t.Errorf("RequestConfirm without listener = %d, want ConfirmNo", resp)
	}
}

// TestExecutionRequestConfirm_ConsumerReceivesRequest verifies that a
// registered consumer receives confirmation requests and its response is
// returned.
func TestExecutionRequestConfirm_ConsumerReceivesRequest(t *testing.T) {
	ec := newEC(internal.ExecutionConfirm)

	received := make(chan internal.ConfirmRequest, 1)
	ec.SetConfirmConsumer(func(ctx context.Context, req internal.ConfirmRequest) error {
		received <- req
		// Hold the request open until the controller cancels the context,
		// which happens once RequestConfirm has consumed the response.
		// Consumers must not receive from req.ResponseChan: the controller
		// is its only reader.
		<-ctx.Done()
		return nil
	})

	respCh := make(chan internal.ConfirmResponse, 1)
	go func() {
		respCh <- ec.RequestConfirm("read", `{"path":"x"}`)
	}()

	var req internal.ConfirmRequest
	select {
	case req = <-received:
	case <-time.After(time.Second):
		t.Fatal("consumer did not receive request")
	}

	if req.ToolName != "read" {
		t.Errorf("ToolName = %q, want read", req.ToolName)
	}
	if req.ToolInput != `{"path":"x"}` {
		t.Errorf("ToolInput = %q, want {\"path\":\"x\"}", req.ToolInput)
	}

	req.ResponseChan <- internal.ConfirmYes

	select {
	case resp := <-respCh:
		if resp != internal.ConfirmYes {
			t.Errorf("RequestConfirm = %d, want ConfirmYes", resp)
		}
	case <-time.After(time.Second):
		t.Fatal("RequestConfirm did not return")
	}
}

// TestExecutionRequestConfirm_ConsumerErrorReturnsNo verifies that an error
// from the consumer is treated as a rejection.
func TestExecutionRequestConfirm_ConsumerErrorReturnsNo(t *testing.T) {
	ec := newEC(internal.ExecutionConfirm)

	ec.SetConfirmConsumer(func(ctx context.Context, req internal.ConfirmRequest) error {
		return fmt.Errorf("consumer error")
	})

	resp := ec.RequestConfirm("read", `{"path":"x"}`)
	if resp != internal.ConfirmNo {
		t.Errorf("RequestConfirm = %d, want ConfirmNo", resp)
	}
}

// TestExecutionRequestConfirm_RouterSoleReaderStress is a regression test for
// the flaky ConfirmYes->ConfirmNo failure: the old "safety net" send could
// inject ConfirmNo after the real response was consumed by the wrong party.
// With the router as the sole reader of ResponseChan and a write-once slot,
// the controller must always return the user's ConfirmYes.
func TestExecutionRequestConfirm_RouterSoleReaderStress(t *testing.T) {
	const iterations = 2000
	for i := 0; i < iterations; i++ {
		ec := newEC(internal.ExecutionConfirm)

		received := make(chan internal.ConfirmRequest, 1)
		ec.SetConfirmConsumer(func(ctx context.Context, req internal.ConfirmRequest) error {
			received <- req
			// Wait for the controller to consume the response; never read
			// req.ResponseChan (the controller is its only reader).
			<-ctx.Done()
			return nil
		})

		respCh := make(chan internal.ConfirmResponse, 1)
		go func() {
			respCh <- ec.RequestConfirm("read", `{"path":"x"}`)
		}()

		select {
		case req := <-received:
			req.ResponseChan <- internal.ConfirmYes
		case <-time.After(time.Second):
			t.Fatalf("iteration %d: consumer did not receive request", i)
		}

		select {
		case resp := <-respCh:
			if resp != internal.ConfirmYes {
				t.Fatalf("iteration %d: RequestConfirm = %d, want ConfirmYes", i, resp)
			}
		case <-time.After(time.Second):
			t.Fatalf("iteration %d: RequestConfirm did not return", i)
		}
	}
}

// TestExecutionRequestConfirm_ResponseSentBeforeConsumerReturn verifies that
// a response sent by the consumer just before it returns is not lost to the
// safe default, regardless of goroutine scheduling.
func TestExecutionRequestConfirm_ResponseSentBeforeConsumerReturn(t *testing.T) {
	const iterations = 500
	for i := 0; i < iterations; i++ {
		ec := newEC(internal.ExecutionConfirm)
		ec.SetConfirmConsumer(func(_ context.Context, req internal.ConfirmRequest) error {
			req.ResponseChan <- internal.ConfirmYes
			return nil
		})

		if resp := ec.RequestConfirm("read", `{"path":"x"}`); resp != internal.ConfirmYes {
			t.Fatalf("iteration %d: RequestConfirm = %d, want ConfirmYes", i, resp)
		}
	}
}

// TestExecutionRequestConfirm_HeadlessConsumerPattern mirrors the production
// headless consumer (internal/app/headless.go runConfirmationReader): it sends
// exactly one response on ResponseChan, then blocks on ctx like a modal would.
// The controller must route that response to RequestConfirm. Stress-tested to
// catch any regression in the send-only contract.
func TestExecutionRequestConfirm_HeadlessConsumerPattern(t *testing.T) {
	const iterations = 2000
	for i := 0; i < iterations; i++ {
		ec := newEC(internal.ExecutionConfirm)
		ec.SetConfirmConsumer(func(ctx context.Context, req internal.ConfirmRequest) error {
			// Send the response (send-only), then hold like a real modal
			// until the controller cancels ctx on completion.
			select {
			case req.ResponseChan <- internal.ConfirmYes:
			case <-ctx.Done():
				return ctx.Err()
			}
			<-ctx.Done()
			return nil
		})

		if resp := ec.RequestConfirm("read", `{"path":"x"}`); resp != internal.ConfirmYes {
			t.Fatalf("iteration %d: RequestConfirm = %d, want ConfirmYes", i, resp)
		}
	}
}

// TestConfirmSlot_FirstWriteWins verifies the write-once semantics of the
// confirmation outcome slot.
func TestConfirmSlot_FirstWriteWins(t *testing.T) {
	slot := newConfirmSlot()
	slot.resolve(internal.ConfirmYes)
	slot.resolve(internal.ConfirmNo) // must be ignored

	select {
	case resp := <-slot.outcome():
		if resp != internal.ConfirmYes {
			t.Errorf("slot outcome = %d, want ConfirmYes", resp)
		}
	default:
		t.Fatal("slot not resolved after first resolve")
	}
}

// TestExecutionRequestConfirm_ConsumerSettlesAfterCancel verifies that the
// controller returns the safe default when the consumer context is cancelled.
// The test drives cancellation through SetConfirmConsumer(nil) to mimic
// consumer removal (e.g., headless shutdown).
func TestExecutionRequestConfirm_ConsumerSettlesAfterCancel(t *testing.T) {
	ec := newEC(internal.ExecutionConfirm)

	wait := make(chan struct{})
	ec.SetConfirmConsumer(func(ctx context.Context, req internal.ConfirmRequest) error {
		close(wait)
		<-ctx.Done()
		return ctx.Err()
	})

	respCh := make(chan internal.ConfirmResponse, 1)
	go func() {
		respCh <- ec.RequestConfirm("read", `{"path":"x"}`)
	}()

	select {
	case <-wait:
	case <-time.After(time.Second):
		t.Fatal("consumer did not start")
	}

	ec.SetConfirmConsumer(nil)

	select {
	case resp := <-respCh:
		if resp != internal.ConfirmNo {
			t.Errorf("RequestConfirm = %d, want ConfirmNo", resp)
		}
	case <-time.After(time.Second):
		t.Fatal("RequestConfirm did not return after consumer removal")
	}
}

// TestExecutionRequestConfirm_YoloBypassesConsumer verifies that yolo mode
// never consults the consumer.
func TestExecutionRequestConfirm_YoloBypassesConsumer(t *testing.T) {
	ec := newEC(internal.ExecutionYolo)

	ec.SetConfirmConsumer(func(ctx context.Context, req internal.ConfirmRequest) error {
		t.Error("consumer should not be called in yolo mode")
		return nil
	})

	resp := ec.RequestConfirm("read", `{"path":"x"}`)
	if resp != internal.ConfirmYes {
		t.Errorf("RequestConfirm = %d, want ConfirmYes", resp)
	}
}

func TestExecutionAutonomy_FallbackToMode(t *testing.T) {
	ec := newEC(internal.ExecutionYolo)
	if ec.Autonomy() != internal.AutonomyYolo {
		t.Errorf("Autonomy() = %q, want %q", ec.Autonomy(), internal.AutonomyYolo)
	}

	ecSolo := newEC(internal.ExecutionSolo)
	if ecSolo.Autonomy() != internal.AutonomySolo {
		t.Errorf("Autonomy() = %q, want %q", ecSolo.Autonomy(), internal.AutonomySolo)
	}

	ec2 := newEC(internal.ExecutionConfirm)
	if ec2.Autonomy() != internal.AutonomyConfirm {
		t.Errorf("Autonomy() = %q, want %q", ec2.Autonomy(), internal.AutonomyConfirm)
	}

	ec3 := newEC(internal.ExecutionReview)
	if ec3.Autonomy() != internal.AutonomyReview {
		t.Errorf("Autonomy() = %q, want %q", ec3.Autonomy(), internal.AutonomyReview)
	}
}

func TestExecutionAutonomy_FromSessionState(t *testing.T) {
	ss := NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyConfirm})
	ec := NewExecutionController(&config.Config{}, ss)

	if ec.Autonomy() != internal.AutonomyConfirm {
		t.Errorf("Autonomy() = %q, want %q", ec.Autonomy(), internal.AutonomyConfirm)
	}

	// ShouldConfirm should use autonomy from session state
	if !ec.ShouldConfirm("read", "test") {
		t.Error("ShouldConfirm should be true with AutonomyConfirm")
	}
}

func TestNewExecutionController_NilConfigDoesNotPanic(t *testing.T) {
	// Regression: previously the constructor dereferenced cfg.Execution.Mode
	// before the cfg != nil guard, so a nil config panicked (staticcheck
	// SA5011). It must now construct cleanly and fall back to safe defaults.
	ec := NewExecutionController(nil, nil)
	if ec == nil {
		t.Fatal("expected non-nil ExecutionController")
	}
	if ec.Autonomy() != internal.AutonomySolo {
		t.Errorf("nil cfg should default to solo autonomy (zero value), got %q", ec.Autonomy())
	}
}
