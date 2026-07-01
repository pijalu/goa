// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package google

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// noFinishReader serves one text chunk then EOF, with NO FinishReason — the
// exact condition that previously left the stream open and hung consumers.
const noFinishStream = "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}]}}]}\n\n"

// TestParseGoogleSSENoFinishReasonTerminatesStream is the regression test for
// AGENT-B3: when content was streamed but the connection closed without a
// FinishReason, the stream used to stay open and Result() blocked forever.
func TestParseGoogleSSENoFinishReasonTerminatesStream(t *testing.T) {
	stream := provider.NewAssistantMessageEventStream(8)

	done := make(chan struct{})
	go func() {
		parseGoogleSSE(io.NopCloser(strings.NewReader(noFinishStream)), stream)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("parseGoogleSSE did not return within deadline")
	}

	resultCh := make(chan *provider.AssistantMessage, 1)
	go func() { resultCh <- stream.Result() }()

	select {
	case res := <-resultCh:
		if res == nil {
			t.Fatal("Result() returned nil AssistantMessage")
		}
		// Content must be preserved even without a FinishReason.
		if len(res.Content) == 0 || res.Content[0].Text != "hi" {
			t.Fatalf("unexpected content: %+v", res.Content)
		}
		if res.StopReason != provider.StopReasonEndTurn {
			t.Fatalf("expected synthesized StopReasonEndTurn, got %v", res.StopReason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Result() did not return within deadline (stream left open)")
	}
}

// TestParseGoogleSSEEmptyStreamTerminates verifies the no-content path still
// terminates the stream (the original !gacc.started branch).
func TestParseGoogleSSEEmptyStreamTerminates(t *testing.T) {
	stream := provider.NewAssistantMessageEventStream(8)

	done := make(chan struct{})
	go func() {
		parseGoogleSSE(io.NopCloser(strings.NewReader("")), stream)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("parseGoogleSSE did not return within deadline")
	}

	select {
	case res := <-resultOf(stream):
		if res == nil {
			t.Fatal("expected empty AssistantMessage, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Result() did not return within deadline")
	}
}

func resultOf(stream *provider.AssistantMessageEventStream) chan *provider.AssistantMessage {
	ch := make(chan *provider.AssistantMessage, 1)
	go func() { ch <- stream.Result() }()
	return ch
}

// TestParseGoogleSSEFinishReasonTerminatesOnce verifies that a normal
// FinishReason-bearing chunk terminates the stream exactly once and endIfOpen
// does not double-fire (End is idempotent regardless).
func TestParseGoogleSSEFinishReasonTerminatesOnce(t *testing.T) {
	input := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"done\"}]},\"finishReason\":\"STOP\"}]}\n\n"
	stream := provider.NewAssistantMessageEventStream(8)

	done := make(chan struct{})
	go func() {
		parseGoogleSSE(io.NopCloser(strings.NewReader(input)), stream)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("parseGoogleSSE did not return within deadline")
	}

	res := stream.Result()
	if res == nil || res.StopReason != provider.StopReasonEndTurn {
		t.Fatalf("unexpected result: %+v", res)
	}
	if len(res.Content) == 0 || res.Content[0].Text != "done" {
		t.Fatalf("unexpected content: %+v", res.Content)
	}
}

// TestParseGoogleSSEMalformedChunkSurfacesError verifies AGENT-B5: a malformed
// data chunk causes the stream to terminate with a descriptive decode error.
func TestParseGoogleSSEMalformedChunkSurfacesError(t *testing.T) {
	input := "data: {broken json}\n\n"
	stream := provider.NewAssistantMessageEventStream(8)

	done := make(chan struct{})
	go func() {
		parseGoogleSSE(io.NopCloser(strings.NewReader(input)), stream)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("parseGoogleSSE did not return within deadline")
	}

	errCh := make(chan error, 1)
	go func() { errCh <- stream.Err() }()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected non-nil descriptive error for malformed chunk, got nil")
		}
		if !strings.Contains(err.Error(), "decode") {
			t.Fatalf("expected decode error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream.Err() did not return within deadline")
	}
}
