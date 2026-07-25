// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"bufio"
	"bytes"
	"io"
)

// sseEvent is one decoded Server-Sent Events record.
type sseEvent struct {
	// Event is the value of the "event:" field (empty = default "message").
	Event string
	// Data is the concatenation of all "data:" fields, joined with "\n".
	Data []byte
	// ID is the value of the "id:" field, used for Last-Event-ID reconnection.
	ID string
	// Retry is the value of the "retry:" field in milliseconds (-1 = unset).
	Retry int
}

// sseScanner incrementally decodes a text/event-stream per the WHATWG SSE
// specification: fields are "name: value" lines terminated by a blank line;
// multi-line data fields accumulate; comment lines starting with ':' are
// ignored. It exists because MCP's HTTP transports deliver JSON-RPC frames as
// SSE "data" payloads, and a tolerant, allocation-light decoder keeps the
// reader path simple.
type sseScanner struct {
	sc *bufio.Scanner
}

// maxSSELine bounds a single SSE line; MCP JSON-RPC frames can be large.
const maxSSELine = 4 * 1024 * 1024

func newSSEScanner(r io.Reader) *sseScanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxSSELine)
	return &sseScanner{sc: sc}
}

// next reads the next complete event. It returns io.EOF when the stream ends
// cleanly and any scanner error otherwise.
func (s *sseScanner) next() (sseEvent, error) {
	var ev sseEvent
	ev.Retry = -1
	var data bytes.Buffer
	have := false
	for s.sc.Scan() {
		line := s.sc.Bytes()
		if len(line) == 0 { // blank line = dispatch
			if have {
				ev.Data = append([]byte(nil), data.Bytes()...)
				return ev, nil
			}
			continue
		}
		if line[0] == ':' { // comment
			continue
		}
		field, value := splitSSEField(line)
		switch field {
		case "event":
			ev.Event = value
			have = true
		case "data":
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
			have = true
		case "id":
			ev.ID = value
			have = true
		case "retry":
			ev.Retry = parseRetry(value)
		}
	}
	if err := s.sc.Err(); err != nil {
		return sseEvent{}, err
	}
	// Flush a trailing event not terminated by a blank line.
	if have {
		ev.Data = append([]byte(nil), data.Bytes()...)
		return ev, nil
	}
	return sseEvent{}, io.EOF
}

// splitSSEField splits "field: value" or "field:value". A line without a colon
// is treated as a field name with an empty value (per spec).
func splitSSEField(line []byte) (field, value string) {
	if i := bytes.IndexByte(line, ':'); i >= 0 {
		field = string(line[:i])
		value = string(line[i+1:])
		// Strip a single leading space after the colon.
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		return field, value
	}
	return string(line), ""
}

func parseRetry(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return -1
		}
		n = n*10 + int(s[i]-'0')
	}
	return n
}
