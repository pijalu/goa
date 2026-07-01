// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package anthropic

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// anthropicSSEState tracks the current event being assembled during SSE parsing.
type anthropicSSEState struct {
	event string
	data  strings.Builder
}

// anthropicSSEFlush dispatches the accumulated event+data to the handler.
func (s *anthropicSSEState) flush(handler func(eventType, data string) error) error {
	if s.event != "" && s.data.Len() > 0 {
		if err := handler(s.event, s.data.String()); err != nil {
			return err
		}
	}
	s.event = ""
	s.data.Reset()
	return nil
}

// parseAnthropicEventStream reads an Anthropic-style SSE stream where each
// event has an "event:" line followed by a "data:" line. If handler returns a
// non-nil error the scan stops and the error is returned to the caller.
func parseAnthropicEventStream(r io.Reader, handler func(eventType, data string) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var st anthropicSSEState
	var flushErr error

	flush := func() {
		if flushErr != nil {
			return
		}
		flushErr = st.flush(handler)
	}

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			flush()
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			flush()
			st.event = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			st.data.WriteString(strings.TrimPrefix(line, "data: "))
		}
	}

	flush()

	if flushErr != nil {
		return flushErr
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("anthropic SSE scanner error: %w", err)
	}
	return nil
}
