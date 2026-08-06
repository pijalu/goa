// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

import (
	"encoding/json"
	"strings"
)

// SafeToolArguments returns a version of tool call arguments that is always
// well-formed JSON, safe to re-serialize into a provider request.
//
// Providers increasingly validate historical tool calls and reject the whole
// request when a past call carries malformed arguments (e.g. poolside 400
// "Invalid JSON in tool call arguments"). A single tool call truncated
// mid-stream would otherwise poison the session permanently: the parse error
// is already reported in the tool result, but re-sending the raw arguments
// fails every subsequent request. Valid arguments pass through unchanged,
// empty arguments normalize to "{}", truncated JSON is repaired by dropping
// the dangling tail and closing open containers, and anything else degrades
// to "{}".
func SafeToolArguments(args string) string {
	if json.Valid([]byte(args)) {
		return args
	}
	if strings.TrimSpace(args) == "" {
		return "{}"
	}
	if repaired, ok := repairTruncatedJSON(args); ok {
		return repaired
	}
	return "{}"
}

// Frame expectations for the repair scanner.
const (
	expectObjKeyOrEnd   uint8 = iota // just after '{'
	expectObjKey                     // after ',' in an object
	expectObjColon                   // after a key string
	expectObjValue                   // after ':'
	expectObjCommaOrEnd              // after a complete object value
	expectArrValueOrEnd              // just after '['
	expectArrValue                   // after ',' in an array
	expectArrCommaOrEnd              // after a complete array element
)

type jsonRepairFrame struct {
	closer byte // '}' or ']'
	state  uint8
}

// jsonRepairScanner scans a possibly-truncated JSON document and records the
// last position where the document can be legally terminated by appending
// only container closers (plus, at EOF, a closing quote for an open string).
type jsonRepairScanner struct {
	stack       []jsonRepairFrame
	rootDone    bool // root value completed
	inString    bool
	escaped     bool
	stringIsKey bool
	term        int    // byte offset of last legal termination point, -1 when none
	termClosers string // container closers (innermost first) to append at term
	stopped     bool   // structural error or incomplete token ended the scan
}

// repairTruncatedJSON makes a best effort to repair JSON truncated mid-stream
// (a provider that stops emitting a tool call's arguments before the closing
// braces). ok=false when no legal termination point exists.
func repairTruncatedJSON(s string) (string, bool) {
	sc := &jsonRepairScanner{term: -1}
	for i := 0; i < len(s) && !sc.stopped; i++ {
		i = sc.scanByte(s, i)
	}
	return sc.result(s)
}

func (sc *jsonRepairScanner) scanByte(s string, i int) int {
	c := s[i]
	if sc.inString {
		sc.scanStringByte(c, i)
		return i
	}
	switch {
	case isJSONWhitespace(c):
		// insignificant whitespace
	case c == '"':
		sc.openString()
	case c == '{' || c == '[':
		sc.openContainer(c, i)
	case c == '}' || c == ']':
		sc.closeContainer(c, i)
	case c == ',':
		sc.scanComma()
	case c == ':':
		sc.scanColon()
	default:
		return sc.scanPrimitive(s, i)
	}
	return i
}

func (sc *jsonRepairScanner) scanStringByte(c byte, i int) {
	switch {
	case sc.escaped:
		sc.escaped = false
	case c == '\\':
		sc.escaped = true
	case c == '"':
		sc.closeString(i + 1)
	}
}

func (sc *jsonRepairScanner) openString() {
	switch {
	case len(sc.stack) > 0 && (sc.top().state == expectObjKeyOrEnd || sc.top().state == expectObjKey):
		sc.stringIsKey = true
		sc.inString = true
	case sc.valueExpected():
		sc.stringIsKey = false
		sc.inString = true
	default:
		// A string where neither a key nor a value is legal (e.g. after the
		// root value completed).
		sc.stopped = true
	}
}

func (sc *jsonRepairScanner) closeString(end int) {
	sc.inString = false
	if sc.stringIsKey {
		sc.top().state = expectObjColon
		return
	}
	sc.markValueComplete(end)
}

func (sc *jsonRepairScanner) openContainer(c byte, i int) {
	if !sc.valueExpected() {
		sc.stopped = true
		return
	}
	frame := jsonRepairFrame{closer: '}', state: expectObjKeyOrEnd}
	if c == '[' {
		frame = jsonRepairFrame{closer: ']', state: expectArrValueOrEnd}
	}
	sc.stack = append(sc.stack, frame)
	// An empty container is always legal, so the position right after the
	// opener is a termination point.
	sc.markTerm(i + 1)
}

func (sc *jsonRepairScanner) closeContainer(c byte, i int) {
	if len(sc.stack) == 0 || sc.top().closer != c || !sc.endAllowed() {
		sc.stopped = true
		return
	}
	sc.stack = sc.stack[:len(sc.stack)-1]
	sc.markValueComplete(i + 1)
}

func (sc *jsonRepairScanner) scanComma() {
	if len(sc.stack) == 0 {
		sc.stopped = true
		return
	}
	switch sc.top().state {
	case expectObjCommaOrEnd:
		sc.top().state = expectObjKey
	case expectArrCommaOrEnd:
		sc.top().state = expectArrValue
	default:
		// Trailing/leading comma: the cut point stays at the last value.
		sc.stopped = true
	}
}

func (sc *jsonRepairScanner) scanColon() {
	if len(sc.stack) == 0 || sc.top().state != expectObjColon {
		sc.stopped = true
		return
	}
	sc.top().state = expectObjValue
}

// scanPrimitive consumes a literal (true/false/null/number) starting at i and
// returns the index of its last byte. An invalid token — garbage, or a token
// truncated at EOF like "tru" — stops the scan without a new cut point.
func (sc *jsonRepairScanner) scanPrimitive(s string, i int) int {
	if !sc.valueExpected() {
		sc.stopped = true
		return i
	}
	j := i
	for j < len(s) && isPrimitiveChar(s[j]) {
		j++
	}
	if !json.Valid([]byte(s[i:j])) {
		sc.stopped = true
		return i
	}
	sc.markValueComplete(j)
	return j - 1
}

func isPrimitiveChar(c byte) bool {
	return c == '-' || c == '+' || c == '.' ||
		(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isJSONWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// valueExpected reports whether the scanner sits where a value may start:
// the root, an array element, or an object value after ':'.
func (sc *jsonRepairScanner) valueExpected() bool {
	if len(sc.stack) == 0 {
		return !sc.rootDone
	}
	st := sc.top().state
	return st == expectArrValueOrEnd || st == expectArrValue || st == expectObjValue
}

func (sc *jsonRepairScanner) endAllowed() bool {
	switch sc.top().state {
	case expectObjKeyOrEnd, expectObjCommaOrEnd, expectArrValueOrEnd, expectArrCommaOrEnd:
		return true
	}
	return false
}

func (sc *jsonRepairScanner) markValueComplete(end int) {
	if len(sc.stack) == 0 {
		sc.rootDone = true
	} else if sc.top().closer == '}' {
		sc.top().state = expectObjCommaOrEnd
	} else {
		sc.top().state = expectArrCommaOrEnd
	}
	sc.markTerm(end)
}

func (sc *jsonRepairScanner) markTerm(end int) {
	sc.term = end
	sc.termClosers = sc.closers()
}

func (sc *jsonRepairScanner) closers() string {
	var b strings.Builder
	for i := len(sc.stack) - 1; i >= 0; i-- {
		b.WriteByte(sc.stack[i].closer)
	}
	return b.String()
}

func (sc *jsonRepairScanner) top() *jsonRepairFrame {
	return &sc.stack[len(sc.stack)-1]
}

func (sc *jsonRepairScanner) result(s string) (string, bool) {
	if sc.inString && !sc.stringIsKey {
		if candidate, ok := sc.closeOpenString(s); ok {
			return candidate, true
		}
	}
	if sc.term < 0 {
		return "", false
	}
	return validCandidate(s[:sc.term] + sc.termClosers)
}

// closeOpenString terminates an open value string at EOF by appending a
// closing quote (dropping a dangling escape backslash first) plus the open
// container closers.
func (sc *jsonRepairScanner) closeOpenString(s string) (string, bool) {
	trimmed := s
	if sc.escaped {
		// The final backslash would escape the synthetic closing quote.
		trimmed = s[:len(s)-1]
	}
	return validCandidate(trimmed + `"` + sc.closers())
}

func validCandidate(candidate string) (string, bool) {
	if !json.Valid([]byte(candidate)) {
		return "", false
	}
	return candidate, true
}
