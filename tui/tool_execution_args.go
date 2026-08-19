// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const defaultToolPreviewLines = 10

// partialStringFieldRe extracts quoted string fields from incomplete JSON
// objects during streaming tool-call argument display.
var partialStringFieldRe = regexp.MustCompile(`"([^"]+)":"((?:\\.|[^"\\])*)`)

func (tc *ToolExecutionComponent) markArgsComplete() {
	if tc.argsComplete {
		return
	}
	tc.argsComplete = true
	if tc.waitStart.IsZero() {
		tc.waitStart = time.Now()
	}
}

// SetArgsPartial updates the header display with partial tool call
// arguments during streaming. Unlike SetArgsJSON, this does NOT attempt
// json.Unmarshal (partial JSON would fail). The renderer handles
// incomplete JSON via the ArgsComplete field in RenderContext.
func (tc *ToolExecutionComponent) SetArgsPartial(args string) {
	tc.toolArgs = args
	tc.updatePartialArgs(args)
	// Update byte/line progress from the content arg so the duration line
	// shows live stats ("elapsed 1.2s · 3.4 KB · 42 lines") during write
	// streaming. This mirrors how SetOutput tracks outputBytes/outputLines
	// for bash/terminal progress events.
	tc.updateContentProgress()
	tc.invalidateBody()
	tc.updateBox()
	tc.Invalidate()
	if tc.onInvalidate != nil {
		tc.onInvalidate()
	}
}

// updateContentProgress extracts byte/line counts from the "content" arg
// (present in write/edit tools) and stores them for progressSuffix().
// Only updates when content is present and growing — avoids flicker when
// other args (path, etc.) arrive after content.
func (tc *ToolExecutionComponent) updateContentProgress() {
	content, ok := tc.args["content"].(string)
	if !ok || content == "" {
		return
	}
	n := len(content)
	if n > tc.outputBytes {
		tc.outputBytes = n
		tc.outputLines = strings.Count(content, "\n")
		if !strings.HasSuffix(content, "\n") {
			tc.outputLines++
		}
	}
}

// couldBeCompleteJSON reports whether raw might be a complete JSON object:
// it ends with '}' after optional whitespace. Mid-stream args end inside a
// string value (a quote, escape, or content byte), so this filters out every
// incomplete delta cheaply, in O(trailing whitespace) time.
func couldBeCompleteJSON(raw string) bool {
	for i := len(raw) - 1; i >= 0; i-- {
		switch raw[i] {
		case ' ', '\t', '\n', '\r':
			continue
		case '}':
			return true
		default:
			return false
		}
	}
	return false
}

// updatePartialArgs merges best-effort parsed fields from a partial JSON
// argument string into tc.args so renderers can display streaming content
// (e.g. write/edit content) before the full JSON is complete.
//
// Streaming args arrive as a growing accumulated JSON prefix, one delta per
// token. A naive re-scan of the whole document per delta is O(n^2). This
// scanner is incremental: it consumes each field once its value terminates,
// keeps partialPos at the first unconsumed field, and for the single still-open
// field decodes only the raw value tail. Per-delta work is proportional to the
// new text plus one linear pass over the open field's value, not the document.
func (tc *ToolExecutionComponent) updatePartialArgs(raw string) {
	// Attempt the real parser only when the document could be complete: the
	// last non-space byte is '}'. Mid-stream the string ends inside a value,
	// so this cheap check avoids a full O(n) json.Unmarshal that always fails
	// (and always re-parses from byte 0) on every delta. SetArgsJSON does the
	// authoritative parse at completion.
	if couldBeCompleteJSON(raw) {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			tc.args = parsed
			return
		}
	}

	// Reset when the args string is replaced rather than grown.
	if !strings.HasPrefix(raw, tc.partialRaw) {
		tc.partialPos, tc.partialKey, tc.partialVFrom = 0, "", 0
		tc.partialVDone, tc.partialValue = 0, ""
		tc.args = nil
	}
	tc.partialRaw = raw
	if tc.args == nil {
		tc.args = make(map[string]any)
	}

	// Consume completed fields starting at partialPos. A field is complete
	// once its value's closing quote is present; consumeField advances
	// partialPos past it and records the decoded value.
	for tc.partialPos < len(raw) {
		next, done := tc.consumePartialField(raw)
		if !done {
			break // tail is an incomplete field; handled below
		}
		tc.partialPos = next
	}
}

// consumePartialField processes input starting at tc.partialPos. It consumes
// every completed `"key":"value"` field and, for the single still-open field at
// the tail, decodes its (growing) raw value into tc.args. It uses a hand-rolled
// scan — not a regexp over the tail — so per-delta work is proportional to the
// newly arrived bytes rather than the accumulated document size.
func (tc *ToolExecutionComponent) consumePartialField(raw string) (next int, done bool) {
	for tc.partialPos < len(raw) {
		key, vStart, vEnd, closed, ok := scanPartialField(raw, tc.partialPos)
		if !ok {
			return tc.partialPos, false // no complete field boundary yet
		}
		if key != tc.partialKey {
			// Moved to a new field: restart its append-only decode.
			tc.partialKey, tc.partialVFrom = key, vStart
			tc.partialVDone, tc.partialValue = 0, ""
		}
		if !closed {
			// Open (still-growing) field: decode only the newly arrived raw
			// suffix and append it, keeping per-delta work O(new bytes) instead
			// of re-decoding the whole value each delta. partialPos stays at
			// the field start so the (now larger) raw range is re-read next
			// delta — but only the unread suffix is decoded.
			tc.appendOpenValue(raw[vStart:vEnd])
			tc.args[key] = tc.partialValue
			return tc.partialPos, false
		}
		// Completed field: decode the full raw value once.
		value := raw[vStart:vEnd]
		if u, err := strconv.Unquote(`"` + value + `"`); err == nil {
			value = u
		}
		tc.args[key] = value
		tc.partialPos = vEnd + 1 // past the closing quote
	}
	return tc.partialPos, true
}

// appendOpenValue decodes the not-yet-consumed raw suffix of the open field's
// value and appends it to partialValue, advancing partialVDone. Decoding only
// the new suffix keeps streaming a large value O(1) per delta rather than
// O(value) per delta. strconv.Unquote needs a balanced escape at the cut
// point, so a trailing incomplete escape is left for the next delta.
func (tc *ToolExecutionComponent) appendOpenValue(rawVal string) {
	if tc.partialVDone >= len(rawVal) {
		return // nothing new (or value shrank; the next full decode corrects it)
	}
	suffix := rawVal[tc.partialVDone:]
	// Don't split a backslash escape at the cut point: if the new suffix ends
	// mid-escape, hold the trailing partial sequence for the next delta.
	if n := trailingBackslashes(suffix); n%2 == 1 {
		suffix = suffix[:len(suffix)-1]
	}
	if suffix == "" {
		return
	}
	decoded := suffix
	if u, err := strconv.Unquote(`"` + suffix + `"`); err == nil {
		decoded = u
	}
	tc.partialValue += decoded
	tc.partialVDone += len(suffix)
}

// trailingBackslashes reports how many backslashes end s.
func trailingBackslashes(s string) int {
	n := 0
	for i := len(s) - 1; i >= 0 && s[i] == '\\'; i-- {
		n++
	}
	return n
}

// scanPartialField scans raw starting at from for the next `"key":"value"`
// field. It returns the key, the value's raw [vStart,vEnd) range, whether the
// value's closing quote was seen (closed), and ok=false when no field boundary
// is found in the remaining input. The value range excludes surrounding quotes.
func scanPartialField(raw string, from int) (key string, vStart, vEnd int, closed, ok bool) {
	keyOpen := nextQuote(raw, from)
	if keyOpen < 0 {
		return "", 0, 0, false, false
	}
	keyClose := nextQuote(raw, keyOpen+1)
	if keyClose < 0 {
		return "", 0, 0, false, false // key not yet terminated
	}
	valOpen := nextQuote(raw, keyClose+1)
	if valOpen < 0 {
		return "", 0, 0, false, false // value opening quote not yet present
	}
	valClose := nextUnescapedQuote(raw, valOpen+1)
	if valClose < 0 {
		// Value runs to end-of-input: still open/growing.
		return raw[keyOpen+1 : keyClose], valOpen + 1, len(raw), false, true
	}
	return raw[keyOpen+1 : keyClose], valOpen + 1, valClose, true, true
}

// nextQuote returns the index of the next `"` at or after pos, or -1.
func nextQuote(raw string, pos int) int {
	for i := pos; i < len(raw); i++ {
		if raw[i] == '"' {
			return i
		}
	}
	return -1
}

// nextUnescapedQuote returns the index of the next `"` not preceded by an odd
// run of backslashes (i.e. a real string terminator), or -1.
func nextUnescapedQuote(raw string, pos int) int {
	for i := pos; i < len(raw); i++ {
		switch raw[i] {
		case '\\':
			i++ // skip the escaped character
		case '"':
			return i
		}
	}
	return -1
}

// SetArgs parses and stores the structured arguments for renderer use.
func (tc *ToolExecutionComponent) SetArgs(args map[string]any) {
	tc.args = args
	tc.invalidateBody()
	tc.updateBox()
	tc.Invalidate()
}

// SetArgsJSON parses JSON arguments and stores them for the renderer.
// When the JSON is successfully parsed, args are marked as complete.
func (tc *ToolExecutionComponent) SetArgsJSON(argsJSON string) {
	tc.partialRaw, tc.partialPos, tc.partialKey, tc.partialVFrom = "", 0, "", 0
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err == nil {
		tc.args = args
		tc.markArgsComplete()
		tc.updateContentProgress()
	}
	tc.toolArgs = FormatToolArgs(tc.toolName, argsJSON)
	tc.invalidateBody()
	tc.updateBox()
	tc.Invalidate()
}

// SetOnInvalidate registers a callback invoked whenever the component's
// internal state changes, allowing the owning viewport to invalidate its
// render cache.
