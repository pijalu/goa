// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"bufio"
	"io"
	"strings"
)

// ParseSSE reads OpenAI-style Server-Sent Events from r and calls emit for
// each payload. It handles the "data: " prefix and stops when it encounters
// "[DONE]". Returns nil on normal completion (either [DONE] or clean EOF), or
// the scanner error if the stream was interrupted by an I/O error.
// Optional done callbacks are invoked when the stream ends because of a
// [DONE] marker, so callers can distinguish a clean end-of-stream sentinel
// from a plain connection close.
//
// Per RFC 9110 / WHATWG server-sent events, consecutive "data:" lines of the
// same event are joined with '\n' before being emitted as one payload: the
// parser buffers consecutive data lines and flushes on any non-data line
// (blank line, "event:", comment) or at clean EOF. Lenient providers that
// omit blank-line separators keep working because each JSON-per-line payload
// is flushed by the next line; only truly consecutive data lines are merged.
func ParseSSE(r io.Reader, emit func(string), done ...func()) error {
	scanner := bufio.NewScanner(r)
	// Increase buffer from default 64KB to 1MB to handle large SSE lines
	// (e.g., long tool call arguments, large content chunks).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var buf strings.Builder
	// pending reports whether buf holds a not-yet-emitted event. Tracked
	// explicitly (not via buf.Len()) so empty data payloads still emit,
	// preserving pre-join behavior for single-line streams.
	pending := false
	// flush emits and clears the buffered multi-data-line event, if any.
	flush := func() {
		if pending {
			emit(buf.String())
			buf.Reset()
			pending = false
		}
	}

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			// Blank line, "event:", comments... dispatch the buffered event.
			flush()
			continue
		}

		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			// Deliver any buffered payload first so no data is dropped, then
			// short-circuit with the done callbacks (unchanged semantics).
			flush()
			for _, d := range done {
				d()
			}
			return nil
		}
		pending = appendSSEData(&buf, pending, payload)
	}
	if err := scanner.Err(); err != nil {
		// Mid-stream I/O failure: surface the error without emitting the
		// incomplete trailing event still sitting in the buffer.
		return err
	}
	flush()
	return nil
}

// appendSSEData appends one data-line payload to the current event buffer,
// inserting the spec-mandated '\n' separator between consecutive data lines.
// It returns the updated pending state.
func appendSSEData(buf *strings.Builder, pending bool, value string) bool {
	if pending {
		buf.WriteByte('\n')
	}
	buf.WriteString(value)
	return true
}
