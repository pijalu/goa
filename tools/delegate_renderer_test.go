// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/tuirender"
)

// TestDelegateRenderer_RenderCall pins the visible spawn feedback: the header
// names the target agent and shows a task preview, so a delegate_to bubble is
// self-explanatory instead of a generic tool box (bugs.md: "delegate_to
// reports success without clear UI feedback").
func TestDelegateRenderer_RenderCall(t *testing.T) {
	r := DelegateRenderer{}
	ctx := tuirender.RenderContext{}

	call := ansi.Strip(r.RenderCall(map[string]any{"agent": "coder", "task": "Refactor the parser"}, ctx))
	if !strings.Contains(call, "delegate") || !strings.Contains(call, "coder") ||
		!strings.Contains(call, "Refactor the parser") {
		t.Fatalf("delegate call header must name agent + task, got %q", call)
	}

	// request_review carries content, not agent/task.
	review := ansi.Strip(r.RenderCall(map[string]any{"content": "critique the diff"}, ctx))
	if !strings.Contains(review, "critique the diff") {
		t.Fatalf("request_review header must show content, got %q", review)
	}

	// A long task is truncated, not wrapped.
	long := ansi.Strip(r.RenderCall(map[string]any{"agent": "coder", "task": strings.Repeat("x", 200)}, ctx))
	if ansi.Width(long) > 100 {
		t.Fatalf("long task must be truncated, got width %d", ansi.Width(long))
	}
}

// TestDelegateRenderer_RenderResult pins the visible ack: the async JSON ack
// renders as a one-line status, not a raw blob.
func TestDelegateRenderer_RenderResult(t *testing.T) {
	r := DelegateRenderer{}
	ctx := tuirender.RenderContext{}

	ack := ansi.Strip(r.RenderResult(`{"status":"delegated","agent":"coder","message":"Task delegated."}`, ctx))
	if !strings.Contains(ack, "delegated") || !strings.Contains(ack, "coder") {
		t.Fatalf("delegated ack must name the agent, got %q", ack)
	}
	if strings.Contains(ack, "{") {
		t.Fatalf("ack must not render as raw JSON, got %q", ack)
	}

	review := ansi.Strip(r.RenderResult(`{"status":"review_complete"}`, ctx))
	if !strings.Contains(review, "review complete") {
		t.Fatalf("review ack, got %q", review)
	}

	// Non-JSON output (defensive) passes through.
	plain := r.RenderResult("some free text", ctx)
	if !strings.Contains(plain, "some free text") {
		t.Fatalf("plain output passes through, got %q", plain)
	}
}
