// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	// loopSampleHead and loopSampleTail are the rune counts kept at the
	// start and end of an elided repeated-sequence sample; the middle of a
	// long repeat is replaced by an "...(N chars)..." marker between them.
	loopSampleHead = 60
	loopSampleTail = 30
)

// elideLoopSample renders a repeated sequence for one-line TUI display
// (runaway-loop visibility: `start of repeat...(x chars)...end of
// repeat`). Whitespace runs collapse to single spaces; when the flattened
// sequence is longer than its elided form, the middle is replaced by an
// "...(N chars)..." marker keeping the head and tail visible. A sequence
// whose elided form would not be shorter is returned as-is — elision only
// kicks in when it actually shortens the display.
func elideLoopSample(s string) string {
	flat := strings.Join(strings.Fields(s), " ")
	r := []rune(flat)
	if len(r) <= loopSampleHead+loopSampleTail {
		return flat
	}
	omitted := len(r) - loopSampleHead - loopSampleTail
	elided := string(r[:loopSampleHead]) + "...(" + strconv.Itoa(omitted) + " chars)..." + string(r[len(r)-loopSampleTail:])
	if len([]rune(elided)) >= len(r) {
		return flat
	}
	return elided
}

// loopEvidenceSuffix formats the elided repeated sequence for appending to a
// guardrail warning or stop message: ` (repeated: "…")`. It returns "" when
// no sample was captured so the base message is unchanged.
func loopEvidenceSuffix(sample string) string {
	if sample == "" {
		return ""
	}
	return fmt.Sprintf(" (repeated: %q)", elideLoopSample(sample))
}

// progressLoopSample extracts the repeated content of an assistant message
// for guardrail evidence: the text content when present, falling back to the
// thinking, then to a tool-call description for text-free repeated turns.
func progressLoopSample(msg Message) string {
	if s := strings.TrimSpace(msg.Content); s != "" {
		return s
	}
	if s := strings.TrimSpace(msg.Thinking); s != "" {
		return s
	}
	if n := len(msg.ToolCalls); n > 0 {
		return fmt.Sprintf("(no text — the response was %d tool call(s))", n)
	}
	return "(empty response)"
}
