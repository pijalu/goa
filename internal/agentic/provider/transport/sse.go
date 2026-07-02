// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package transport

import (
	"bufio"
	"io"
	"strings"
)

// SSEEvent is a parsed Server-Sent Events line group.
type SSEEvent struct {
	Event string
	Data  string
}

// sseMaxLineBytes is the maximum size of a single SSE line. The bufio.Scanner
// default of 64KB is too small for LLM streams where one `data:` line can carry
// a large tool-call argument, a big content/reasoning chunk, or a batched
// server flush. 1MB matches the provider-level ParseSSE and avoids a silent
// bufio.ErrTooLong truncation.
const sseMaxLineBytes = 1024 * 1024

// ParseSSE reads Server-Sent Events from r and yields parsed events. It returns
// the scanner error (if any) so callers can surface I/O failures — such as an
// idle-timeout (ErrStreamIdle) or a connection drop — instead of treating them
// as a clean end-of-stream. Silently ignoring the error here previously caused
// stalled/truncated LLM streams to finalize as if the model had finished,
// ending the turn with no content, no tool calls, and no retry.
func ParseSSE(r io.Reader, yield func(SSEEvent) bool) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), sseMaxLineBytes)
	var current SSEEvent
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			current = emitIfNonEmpty(current, yield)
			continue
		}
		current = parseSSELine(current, line)
	}
	emitIfNonEmpty(current, yield)
	return scanner.Err()
}

func parseSSELine(current SSEEvent, line string) SSEEvent {
	if strings.HasPrefix(line, "event: ") {
		current.Event = strings.TrimPrefix(line, "event: ")
		return current
	}
	if strings.HasPrefix(line, "data: ") {
		current.Data = appendData(current.Data, strings.TrimPrefix(line, "data: "))
	}
	return current
}

func appendData(existing, value string) string {
	if existing == "" {
		return value
	}
	return existing + "\n" + value
}

func emitIfNonEmpty(current SSEEvent, yield func(SSEEvent) bool) SSEEvent {
	if current.Event == "" && current.Data == "" {
		return SSEEvent{}
	}
	yield(current)
	return SSEEvent{}
}
