// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// Regression tests for the runaway-loop TUI visibility bug (bugs.md
// 2026-08-03): the guardrail stopped a session without ever showing WHAT
// was judged a loop, so the user could not tell a genuine loop from a false
// positive. Every guardrail message — the soft stream-loop warning, the
// stream-loop terminal error, the progress-loop warning, the progress-loop
// terminal error, and the latched-session rejection — must carry the
// (elided) repeated sequence.

// loopEvidenceStrings returns the snippets a message must contain to prove it
// shows the elided form of repeat: the visible head, the elision marker, and
// the visible tail.
func loopEvidenceStrings(repeat string) (head, marker, tail string) {
	return repeat[:60], " chars)...", repeat[len(repeat)-30:]
}

// systemEventContains reports whether events include a system content event
// whose text holds every snippet (used to find the visible warning).
func systemEventContains(events []OutputEvent, snippets ...string) bool {
	for _, ev := range events {
		if ev.Type != EventContent || ev.Role != System {
			continue
		}
		found := true
		for _, s := range snippets {
			if !strings.Contains(ev.Text, s) {
				found = false
				break
			}
		}
		if found {
			return true
		}
	}
	return false
}

// requireErrorContainsAll fails the test when err is nil or its message
// lacks any of the wanted snippets.
func requireErrorContainsAll(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want message containing %q", wants)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message missing %q: %v", want, err)
		}
	}
}

// TestProgressLoop_MessagesShowRepeatedSequence covers the turn-level
// detector (checkProgressLoop / checkLoopStopped): the first repeat must
// produce a VISIBLE TUI warning (previously only an ephemeral model hint),
// and both the terminal stop and the latched rejection must name the
// repeated response.
func TestProgressLoop_MessagesShowRepeatedSequence(t *testing.T) {
	repeat := "ALPHA the analysis concluded that the parser handles every grammar rule correctly and consistently across all runs and all regression tests keep passing OMEGA"
	if len(repeat) < 120 {
		t.Fatalf("test fixture must exceed the elision threshold, got %d chars", len(repeat))
	}
	head, marker, tail := loopEvidenceStrings(repeat)

	agent, _ := newLoopScriptAgent(t, []string{repeat, repeat, repeat})
	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	ctx := context.Background()

	if err := agent.Run(ctx, "turn 1"); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if err := agent.Run(ctx, "turn 2"); err != nil {
		t.Fatalf("turn 2 (first repeat = warning only): %v", err)
	}

	// The first repeat must surface a VISIBLE warning event naming the
	// repeated response — before the stop, not only an ephemeral model hint.
	if !systemEventContains(obs.Events(), head, marker, tail) {
		t.Errorf("no visible warning event with the elided repeated sequence after the first repeat; events: %v", obs.Events())
	}

	// The terminal stop must name the repeated response (elided).
	requireErrorContainsAll(t, agent.Run(ctx, "turn 3"), "runaway loop detected", head, marker, tail)

	// The latched-session rejection must keep naming the repeated response.
	requireErrorContainsAll(t, agent.Run(ctx, "turn 4"), "session stopped due to a runaway loop", head)
}

// TestStreamLoopStrike_MessagesShowRepeatedSequence covers the stream-level
// detector (checkStreamLoop → handleStreamLoopStrike): both the soft
// warning event and the terminal error must show the repeated text.
func TestStreamLoopStrike_MessagesShowRepeatedSequence(t *testing.T) {
	// A unit of exactly streamLoopExactMinPeriod chars: Detector A fires at
	// the smallest qualifying period, so the captured sample is the full
	// unit (deterministic assertions). Six copies chain above the default
	// five-repeat threshold.
	unit := "the quick brown fox jumps over the lazy dog while parsing go"
	if len(unit) != streamLoopExactMinPeriod {
		t.Fatalf("fixture unit must be exactly %d chars for a deterministic sample, got %d", streamLoopExactMinPeriod, len(unit))
	}
	// Space-joined copies: the last unit ends the buffer on a word boundary,
	// so the captured sample is exactly the unit (no boundary snap).
	loopText := unit + strings.Repeat(" "+unit, 5)
	ctx := context.Background()

	// Soft strike: the visible warning event must name the repeated unit.
	agent := NewAgent(Config{SystemPrompt: "test", Logger: NewLogger(Error)})
	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	agent.checkStreamLoop(loopText)
	cont, err := agent.handleStreamLoopStrike(ctx)
	if err != nil {
		t.Fatalf("soft strike = %v, want no error", err)
	}
	if !cont {
		t.Fatal("soft strike must report that the turn continues")
	}
	if !systemEventContains(obs.Events(), "Stream loop detected (warning 1 of 3)", unit) {
		t.Errorf("soft-strike warning event does not show the repeated unit; events: %v", obs.Events())
	}

	// Terminal strike: the error must name the repeated unit.
	agent = NewAgent(Config{SystemPrompt: "test", Logger: NewLogger(Error), StreamLoopMaxStrikes: 1})
	agent.checkStreamLoop(loopText)
	_, err = agent.handleStreamLoopStrike(ctx)
	if err == nil || !strings.Contains(err.Error(), "stream loop detected") {
		t.Fatalf("terminal strike = %v, want stream loop detected error", err)
	}
	if !strings.Contains(err.Error(), unit) {
		t.Errorf("terminal stream-loop error does not show the repeated unit: %v", err)
	}
}

// TestStreamLoopStrike_TerminalErrorElidesLongRepeat verifies that a
// kilobyte-scale repeated block is shown ELIDED in the terminal error: the
// elision marker is present, the middle of the block is cut, and the
// message stays one bounded line instead of dumping the full repeat.
func TestStreamLoopStrike_TerminalErrorElidesLongRepeat(t *testing.T) {
	var sb strings.Builder
	for i := 0; sb.Len() < 1300; i++ {
		fmt.Fprintf(&sb, "w%dx%d ", i, i*i)
	}
	block := sb.String()[:1200]
	middle := block[600:660]
	agent := NewAgent(Config{SystemPrompt: "test", Logger: NewLogger(Error), StreamLoopMaxStrikes: 1})
	agent.checkStreamLoop(block + block)
	_, err := agent.handleStreamLoopStrike(context.Background())
	if err == nil || !strings.Contains(err.Error(), "stream loop detected") {
		t.Fatalf("terminal strike = %v, want stream loop detected error", err)
	}
	if !strings.Contains(err.Error(), " chars)...") {
		t.Errorf("terminal error missing elision marker for a long repeat: %v", err)
	}
	if strings.Contains(err.Error(), middle) {
		t.Errorf("terminal error dumps the middle of a long repeat instead of eliding it: %v", err)
	}
	if len(err.Error()) > 400 {
		t.Errorf("terminal error too long for TUI display (%d chars): %v", len(err.Error()), err)
	}
}

// TestElideLoopSample covers the display elision helper: short sequences are
// kept as-is, long ones are elided as `head...(N chars)...tail`, and the
// boundary kicks in exactly when the elided form becomes shorter than the
// raw sequence (head=60, tail=30: at 106 runes the elided form is 106 runes
// — not shorter — so 107 runes is the first elided length).
func TestElideLoopSample(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"short kept as-is", "brown fox jumps over the lazy dog", "brown fox jumps over the lazy dog"},
		{"whitespace collapsed to one line", "line one\nline   two\t\tthree\n\nfour", "line one line two three four"},
		{"exactly head+tail kept", strings.Repeat("x", 90), strings.Repeat("x", 90)},
		{"boundary: 106 runes kept (elision would not shorten)", strings.Repeat("x", 106), strings.Repeat("x", 106)},
		{
			"boundary: 107 runes elided",
			strings.Repeat("x", 107),
			strings.Repeat("x", 60) + "...(17 chars)..." + strings.Repeat("x", 30),
		},
		{
			"long elided with head, marker and tail",
			strings.Repeat("h", 60) + strings.Repeat("m", 500) + strings.Repeat("t", 30),
			strings.Repeat("h", 60) + "...(500 chars)..." + strings.Repeat("t", 30),
		},
		{
			"multibyte runes never split",
			strings.Repeat("é", 200),
			strings.Repeat("é", 60) + "...(110 chars)..." + strings.Repeat("é", 30),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := elideLoopSample(tt.in); got != tt.want {
				t.Errorf("elideLoopSample() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestElideLoopSample_LongMultilineFlattened verifies a long multi-line
// repeat is both elided and flattened to a single display line.
func TestElideLoopSample_LongMultilineFlattened(t *testing.T) {
	in := "start of the repeated block\nwith a second line\n" + strings.Repeat("filler ", 100) + "\nthe final line of the repeat"
	got := elideLoopSample(in)
	if !strings.HasPrefix(got, "start of the repeated block with a second line filler filler") ||
		!strings.Contains(got, " chars)...") ||
		!strings.HasSuffix(got, "the final line of the repeat") ||
		strings.Contains(got, "\n") {
		t.Errorf("elideLoopSample() = %q, want flattened head+marker+tail", got)
	}
}

// TestLoopEvidenceSuffix covers the message suffix: empty when no sample was
// captured, otherwise a quoted (elided) repeated sequence.
func TestLoopEvidenceSuffix(t *testing.T) {
	if got := loopEvidenceSuffix(""); got != "" {
		t.Errorf("empty sample = %q, want empty suffix", got)
	}
	got := loopEvidenceSuffix("the repeated unit")
	if !strings.Contains(got, `(repeated: "the repeated unit")`) {
		t.Errorf("suffix = %q, want quoted sample", got)
	}
	long := strings.Repeat("y", 300)
	got = loopEvidenceSuffix(long)
	if strings.Contains(got, long) || !strings.Contains(got, " chars)...") {
		t.Errorf("suffix for long sample must be elided, got %q", got)
	}
}

// TestProgressLoopSample covers the repeated-content extraction: content
// wins, thinking is the fallback, tool-call-only and empty turns get a
// descriptive placeholder so the evidence is never blank.
func TestProgressLoopSample(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
		want string
	}{
		{"content", Message{Role: Assistant, Content: "  the answer  "}, "the answer"},
		{"thinking fallback", Message{Role: Assistant, Thinking: " deep thought "}, "deep thought"},
		{
			"tool-call-only fallback",
			Message{Role: Assistant, ToolCalls: []ToolCallInfo{{}, {}}},
			"(no text — the response was 2 tool call(s))",
		},
		{"empty response", Message{Role: Assistant}, "(empty response)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := progressLoopSample(tt.msg); got != tt.want {
				t.Errorf("progressLoopSample() = %q, want %q", got, tt.want)
			}
		})
	}
}
