// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package anthropic

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// anthropicStream emits event:/data: pairs. The malformed payload is a
// deliberately broken content_block_start JSON object.
const malformedAnthropicStream = "event: content_block_start\n" +
	"data: {not valid json\n\n" +
	"event: message_delta\n" +
	"data: {\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n"

func TestParseAnthropicSSEMalformedChunkSurfacesError(t *testing.T) {
	stream := provider.NewAssistantMessageEventStream(8)

	done := make(chan struct{})
	go func() {
		parseAnthropicSSE(io.NopCloser(strings.NewReader(malformedAnthropicStream)), stream)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("parseAnthropicSSE did not return within deadline")
	}

	errCh := make(chan error, 1)
	go func() { errCh <- stream.Err() }()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected non-nil descriptive error for malformed chunk, got nil")
		}
		if !strings.Contains(err.Error(), "decode") && !strings.Contains(err.Error(), "anthropic") {
			t.Fatalf("expected descriptive decode error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream.Err() did not return within deadline")
	}
}

// TestParseAnthropicEventStreamHandlerError verifies that a handler-returned
// error stops scanning and propagates.
func TestParseAnthropicEventStreamHandlerError(t *testing.T) {
	input := "event: foo\ndata: bar\n\nevent: baz\ndata: qux\n\n"
	calls := 0
	err := parseAnthropicEventStream(strings.NewReader(input), func(eventType, data string) error {
		calls++
		if eventType == "baz" {
			return io.ErrUnexpectedEOF
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected propagated error, got nil")
	}
	if calls != 2 {
		t.Fatalf("expected scan to stop after 2nd event, got %d calls", calls)
	}
}
