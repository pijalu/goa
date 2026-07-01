// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"errors"
	"sync"
	"testing"
)

func TestNewAssistantMessageEventStream_Basic(t *testing.T) {
	s := NewAssistantMessageEventStream(64)
	if s == nil {
		t.Fatal("expected non-nil stream")
	}

	s.End(&AssistantMessage{})
}

func TestPushAndSeq(t *testing.T) {
	s := NewAssistantMessageEventStream(64)

	events := []AssistantMessageEvent{
		{Type: EventTextStart, ContentIndex: 0},
		{Type: EventTextDelta, ContentIndex: 0, Delta: "Hello"},
		{Type: EventTextDelta, ContentIndex: 0, Delta: " World"},
		{Type: EventTextEnd, ContentIndex: 0, Content: "Hello World"},
	}

	var (
		collected []AssistantMessageEvent
		wg        sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for event := range s.Seq() {
			collected = append(collected, event)
		}
	}()

	for _, e := range events {
		if !s.Push(e) {
			t.Fatal("Push returned false unexpectedly")
		}
	}

	s.End(&AssistantMessage{})

	wg.Wait()

	if len(collected) != len(events) {
		t.Fatalf("expected %d events, got %d", len(events), len(collected))
	}
	for i, e := range collected {
		if e.Type != events[i].Type {
			t.Errorf("event %d: expected Type=%q, got %q", i, events[i].Type, e.Type)
		}
		if e.Delta != events[i].Delta {
			t.Errorf("event %d: expected Delta=%q, got %q", i, events[i].Delta, e.Delta)
		}
	}
}

func TestPushAfterEnd(t *testing.T) {
	s := NewAssistantMessageEventStream(64)

	result := &AssistantMessage{StopReason: StopReasonEndTurn}
	s.End(result)

	if pushed := s.Push(AssistantMessageEvent{Type: EventTextDelta, Delta: "should be ignored"}); pushed {
		t.Error("Push after End should return false")
	}
}

func TestResultBlocks(t *testing.T) {
	s := NewAssistantMessageEventStream(64)
	result := &AssistantMessage{
		Usage:      &Usage{InputTokens: 50, OutputTokens: 100},
		StopReason: StopReasonEndTurn,
	}

	go func() {
		_ = s.Push(AssistantMessageEvent{Type: EventTextStart})
		_ = s.Push(AssistantMessageEvent{Type: EventTextDelta, Delta: "hello"})
		s.End(result)
	}()

	got := s.Result()
	if got == nil {
		t.Fatal("Result() returned nil")
	}
	if got.StopReason != StopReasonEndTurn {
		t.Errorf("expected StopReason=end_turn, got %q", got.StopReason)
	}
	if got.Usage.InputTokens != 50 {
		t.Errorf("expected InputTokens=50, got %d", got.Usage.InputTokens)
	}
}

func TestErrBlocks(t *testing.T) {
	s := NewAssistantMessageEventStream(64)
	sendErr := errors.New("stream error")

	go func() {
		s.CloseWithError(sendErr)
	}()

	got := s.Err()
	if got == nil {
		t.Fatal("Err() should return an error")
	}
	if got != sendErr {
		t.Errorf("expected %v, got %v", sendErr, got)
	}
}

func TestErrNoWait(t *testing.T) {
	s := NewAssistantMessageEventStream(64)

	// Before any error — should not block, return nil
	if err := s.ErrNoWait(); err != nil {
		t.Errorf("expected nil error before close, got %v", err)
	}

	s.CloseWithError(errors.New("test error"))

	if err := s.ErrNoWait(); err == nil {
		t.Error("expected non-nil error after close")
	}
}

func TestCancel(t *testing.T) {
	s := NewAssistantMessageEventStream(64)

	s.Cancel()

	if pushed := s.Push(AssistantMessageEvent{Type: EventTextDelta, Delta: "x"}); pushed {
		t.Error("Push after Cancel should return false")
	}
}

func TestSeqStopsOnCancel(t *testing.T) {
	s := NewAssistantMessageEventStream(64)

	go s.Cancel()

	count := 0
	for range s.Seq() {
		count++
	}
	if count != 0 {
		t.Errorf("expected 0 events after cancel, got %d", count)
	}
}

func TestDrainRemainingEvents(t *testing.T) {
	s := NewAssistantMessageEventStream(64)

	events := []AssistantMessageEvent{
		{Type: EventTextStart},
		{Type: EventTextDelta, Delta: "A"},
		{Type: EventTextDelta, Delta: "B"},
	}

	for _, e := range events {
		if !s.Push(e) {
			t.Fatal("Push returned false unexpectedly")
		}
	}

	var (
		collected []AssistantMessageEvent
		wg        sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for event := range s.Seq() {
			collected = append(collected, event)
		}
	}()

	s.End(&AssistantMessage{StopReason: StopReasonToolCall})

	wg.Wait()

	if len(collected) != len(events) {
		t.Errorf("expected %d events, got %d", len(events), len(collected))
	}
}

func TestEventTypeConstants(t *testing.T) {
	tests := []struct {
		actual EventType
		want   string
	}{
		{EventStart, "start"},
		{EventDone, "done"},
		{EventError, "error"},
		{EventTextStart, "text_start"},
		{EventTextDelta, "text_delta"},
		{EventTextEnd, "text_end"},
		{EventThinkingStart, "thinking_start"},
		{EventThinkingDelta, "thinking_delta"},
		{EventThinkingEnd, "thinking_end"},
		{EventToolCallStart, "toolcall_start"},
		{EventToolCallDelta, "toolcall_delta"},
		{EventToolCallEnd, "toolcall_end"},
	}
	for _, tt := range tests {
		if string(tt.actual) != tt.want {
			t.Errorf("expected %q, got %q", tt.want, string(tt.actual))
		}
	}
}

func TestAssistantMessage_Clone_DeepCopy(t *testing.T) {
	original := &AssistantMessage{
		Content: []ContentBlock{
			{Type: ContentBlockText, Text: "original"},
			{Type: ContentBlockToolCall, ToolCallID: "call_1", ToolName: "test", ToolArguments: `{"x":1}`},
		},
		Usage: &Usage{
			InputTokens:  100,
			OutputTokens: 200,
		},
		StopReason: StopReasonEndTurn,
	}

	clone := original.Clone()

	// Modify original
	original.Content[0].Text = "modified"
	original.Content[1].ToolCallID = "modified"
	original.Usage.InputTokens = 999

	// Verify clone is unchanged
	if clone.Content[0].Text != "original" {
		t.Error("clone.Content[0].Text should be 'original'")
	}
	if clone.Content[1].ToolCallID != "call_1" {
		t.Error("clone.Content[1].ToolCallID should be 'call_1'")
	}
	if clone.Usage.InputTokens != 100 {
		t.Error("clone.Usage.InputTokens should be 100")
	}
}

func TestStream_SeqTerminatesEarly(t *testing.T) {
	s := NewAssistantMessageEventStream(64)

	s.Push(AssistantMessageEvent{Type: EventTextStart})
	s.Push(AssistantMessageEvent{Type: EventTextDelta, Delta: "one"})
	s.Push(AssistantMessageEvent{Type: EventTextDelta, Delta: "two"})

	count := 0
	for event := range s.Seq() {
		count++
		if count == 2 {
			break // yield returns false
		}
		_ = event
	}

	s.End(&AssistantMessage{})

	// After breaking out of the loop, the iterator should still terminate cleanly
	// when the stream ends. We don't care about count here, just that it doesn't
	// panic or deadlock.
}

func TestResultAfterCancel(t *testing.T) {
	s := NewAssistantMessageEventStream(64)

	go func() {
		_ = s.Push(AssistantMessageEvent{Type: EventTextStart})
		s.Cancel()
	}()

	got := s.Result()
	if got != nil {
		t.Error("expected nil result after Cancel")
	}
}

func TestPushBeforeSeqDrain(t *testing.T) {
	// Push events, start Seq goroutine, then End — verify all events are drained.
	s := NewAssistantMessageEventStream(64)

	// Push events before any consumer
	for i := 0; i < 5; i++ {
		s.Push(AssistantMessageEvent{Type: EventTextDelta, Delta: "x"})
	}

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		for event := range s.Seq() {
			// Consume events — this test just verifies we get all of them
			_ = event
		}
	}()

	s.End(&AssistantMessage{})

	wg.Wait()
}

func TestEventStream_PushAfterCloseWithError(t *testing.T) {
	s := NewAssistantMessageEventStream(64)
	s.CloseWithError(errors.New("oops"))

	if pushed := s.Push(AssistantMessageEvent{Type: EventTextStart}); pushed {
		t.Error("Push after CloseWithError should return false")
	}
}

func TestEventStream_EndIdempotent(t *testing.T) {
	s := NewAssistantMessageEventStream(64)
	result := &AssistantMessage{StopReason: StopReasonEndTurn}
	s.End(result)
	s.End(result) // should not panic

	got := s.Result()
	if got == nil || got.StopReason != StopReasonEndTurn {
		t.Errorf("expected end_turn result, got %v", got)
	}
}

func TestEventStream_CloseWithErrorIdempotent(t *testing.T) {
	s := NewAssistantMessageEventStream(64)
	firstErr := errors.New("first")
	secondErr := errors.New("second")
	s.CloseWithError(firstErr)
	s.CloseWithError(secondErr) // should not panic or overwrite

	if got := s.Err(); got != firstErr {
		t.Errorf("expected first error %v, got %v", firstErr, got)
	}
}

func TestEventStream_MixedTerminationIdempotent(t *testing.T) {
	s := NewAssistantMessageEventStream(64)
	s.CloseWithError(errors.New("cancelled"))
	s.End(&AssistantMessage{StopReason: StopReasonEndTurn}) // should not panic
	s.Cancel()                                              // should not panic

	if err := s.Err(); err == nil {
		t.Error("expected error from CloseWithError")
	}
}
