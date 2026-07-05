// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

// Filmstrip is the agent-testable view of a TUI as it evolves over time.
//
// It is the missing piece that makes the TUI testable without a real
// terminal: instead of asserting on escape sequences (which are
// protocol-dependent, layout-fragile, and impossible for an AI agent to
// reason about), tests and agents capture one Snapshot per logical step and
// inspect the structured AgentFrame plus a compact diff against the previous
// step. An agent can "view" the entire UI evolution — the series of widget
// states and the change between each pair — exactly as a human would see it
// on screen, but as data.
//
// Filmstrip owns no rendering state; it only records AgentFrame values that
// the Compositor/engine already produce. It is therefore safe to use from
// tests, golden-file generators, and AI-driven UI introspection tools alike.
type Filmstrip struct {
	frames []Snapshot
}

// Snapshot is one captured UI state in a Filmstrip, paired with the diff that
// describes how it changed relative to the previous Snapshot.
type Snapshot struct {
	Step  int    // 0-indexed position within the filmstrip
	Label string // optional human-readable description of the step
	Frame AgentFrame
	Diff  FrameDiff
}

// FrameDiff summarizes what changed between two consecutive Snapshots. It is
// deliberately compact and content-oriented (not byte/escape oriented) so an
// agent can scan it quickly: "what lines appeared, what disappeared, did the
// status/spinner region change, did the cursor move".
type FrameDiff struct {
	// Lines now visible that were not visible in the previous snapshot
	// (compared by trimmed, ANSI-stripped text).
	AddedLines []string
	// Lines that were visible before but are gone now.
	RemovedLines []string
	// StatusText is the status-spinner text (StatusMsg.Text()) captured for
	// this snapshot, or empty if the spinner is hidden. Because the spinner is
	// the canonical "current activity" indicator, tracking it across steps is
	// the primary way to assert on activity lifecycle (e.g. "the spinner
	// stayed visible after the tool call").
	StatusText string
	// CursorMoved reports whether the absolute cursor position changed.
	CursorMoved bool
}

// NewFilmstrip returns an empty Filmstrip.
func NewFilmstrip() *Filmstrip { return &Filmstrip{} }

// Capture appends a new Snapshot of the given frame. The diff is computed
// against the previous snapshot (or zero-valued if this is the first). The
// label is an optional free-form description of the step that produced this
// frame (e.g. "after tool_call(read)").
func (f *Filmstrip) Capture(label string, frame AgentFrame, statusText string) Snapshot {
	step := len(f.frames)
	var diff FrameDiff
	if step == 0 {
		diff = diffAgainstNil(frame)
	} else {
		diff = diffFrames(f.frames[step-1].Frame, frame)
	}
	diff.StatusText = statusText
	snap := Snapshot{Step: step, Label: label, Frame: frame, Diff: diff}
	f.frames = append(f.frames, snap)
	return snap
}

// Frames returns all captured snapshots.
func (f *Filmstrip) Frames() []Snapshot { return f.frames }

// Last returns the most recent snapshot, or nil if none was captured.
func (f *Filmstrip) Last() *Snapshot {
	if len(f.frames) == 0 {
		return nil
	}
	return &f.frames[len(f.frames)-1]
}

// StatusTrace returns the sequence of status-spinner texts across all
// snapshots, in capture order. Empty strings denote a hidden spinner. This is
// the single most useful artifact for activity-lifecycle assertions: an agent
// can read it to verify the spinner never went dark in the middle of a turn.
func (f *Filmstrip) StatusTrace() []string {
	out := make([]string, len(f.frames))
	for i, s := range f.frames {
		out[i] = s.Diff.StatusText
	}
	return out
}

// Render produces a human-readable, ANSI-free transcript of the filmstrip:
// each step's label, its status text, and the added/removed visible lines.
// Intended for test failure output, golden files, and agent introspection.
func (f *Filmstrip) Render() string {
	var b strings.Builder
	for _, s := range f.frames {
		b.WriteString("=== step ")
		b.WriteString(stepToa(s.Step))
		if s.Label != "" {
			b.WriteString(": ")
			b.WriteString(s.Label)
		}
		b.WriteString(" ===\n")
		if s.Diff.StatusText != "" {
			b.WriteString("status: ")
			b.WriteString(s.Diff.StatusText)
			b.WriteString("\n")
		}
		for _, l := range s.Diff.AddedLines {
			b.WriteString("+ ")
			b.WriteString(l)
			b.WriteString("\n")
		}
		for _, l := range s.Diff.RemovedLines {
			b.WriteString("- ")
			b.WriteString(l)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// diffFrames computes the content diff between two consecutive frames by
// comparing their Visible viewports as multisets of non-empty trimmed lines.
// This intentionally ignores vertical position (which shifts as the chat
// scrolls) and focuses on presence/absence of content — the property an agent
// cares about when asking "did the tool result appear? did the spinner line
// vanish?".
func diffFrames(prev, cur AgentFrame) FrameDiff {
	var d FrameDiff
	prevSet := lineSet(prev.Visible)
	curSet := lineSet(cur.Visible)
	for l := range curSet {
		if !prevSet[l] {
			d.AddedLines = append(d.AddedLines, l)
		}
	}
	for l := range prevSet {
		if !curSet[l] {
			d.RemovedLines = append(d.RemovedLines, l)
		}
	}
	sortStableLines(d.AddedLines)
	sortStableLines(d.RemovedLines)
	d.CursorMoved = !cursorEq(prev.Cursor, cur.Cursor)
	return d
}

// diffAgainstNil treats the previous frame as empty: every visible line is
// "added".
func diffAgainstNil(frame AgentFrame) FrameDiff {
	var d FrameDiff
	for _, l := range frame.Visible {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		d.AddedLines = append(d.AddedLines, t)
	}
	d.CursorMoved = frame.Cursor != nil
	return d
}

// lineSet builds a set of non-empty, ANSI-stripped, trimmed visible lines.
// ANSI stripping is defensive: Visible is already stripped, but a component
// may embed escapes that survive the viewport pass.
func lineSet(visible []string) map[string]bool {
	set := make(map[string]bool, len(visible))
	for _, l := range visible {
		t := strings.TrimSpace(ansi.Strip(l))
		if t == "" {
			continue
		}
		set[t] = true
	}
	return set
}

func cursorEq(a, b *CursorPos) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Row == b.Row && a.Col == b.Col
}

// sortStableLines sorts a slice of lines in a deterministic order (lexical)
// so filmstrip diffs are stable across runs and easy to scan. Small N: a
// simple insertion sort avoids the sort import for this single use.
func sortStableLines(lines []string) {
	for i := 1; i < len(lines); i++ {
		for j := i; j > 0 && lines[j-1] > lines[j]; j-- {
			lines[j-1], lines[j] = lines[j], lines[j-1]
		}
	}
}

// stepToa is a dependency-free int->string for the small step indices used
// in Render, avoiding a name collision with selector.itoa.
func stepToa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
