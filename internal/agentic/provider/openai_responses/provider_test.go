// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package openairesponses

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestParseResponsesSSEMalformedChunkSurfacesError verifies AGENT-B5: a
// malformed data chunk in the OpenAI Responses stream terminates the stream
// with a descriptive decode error.
func TestParseResponsesSSEMalformedChunkSurfacesError(t *testing.T) {
	input := "data: {not valid json}\n\n"
	stream := provider.NewAssistantMessageEventStream(8)

	done := make(chan struct{})
	go func() {
		parseResponsesSSE(io.NopCloser(strings.NewReader(input)), stream)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("parseResponsesSSE did not return within deadline")
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

// TestParseResponsesSSENoCompletionTerminatesStream verifies that a stream
// which streams text but closes without a response.completed event still
// terminates (mirrors AGENT-B3 robustness for the responses parser).
func TestParseResponsesSSENoCompletionTerminatesStream(t *testing.T) {
	input := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"
	stream := provider.NewAssistantMessageEventStream(8)

	done := make(chan struct{})
	go func() {
		parseResponsesSSE(io.NopCloser(strings.NewReader(input)), stream)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("parseResponsesSSE did not return within deadline")
	}

	resCh := make(chan *provider.AssistantMessage, 1)
	go func() { resCh <- stream.Result() }()
	select {
	case res := <-resCh:
		if res == nil || len(res.Content) == 0 || res.Content[0].Text != "hi" {
			t.Fatalf("unexpected result: %+v", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Result() did not return within deadline (stream left open)")
	}
}
