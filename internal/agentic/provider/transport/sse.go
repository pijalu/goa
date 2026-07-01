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

// ParseSSE reads Server-Sent Events from r and yields parsed events.
func ParseSSE(r io.Reader, yield func(SSEEvent) bool) {
	scanner := bufio.NewScanner(r)
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
