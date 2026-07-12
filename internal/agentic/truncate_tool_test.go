// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"strings"
	"testing"
)

// TestTruncateToolResult_PreservesHeadAndTail verifies source-level tool-result
// truncation keeps both the start and the end (4.4c): the beginning matters for
// read_file-style context, the end matters for bash/webfetch errors and final
// results. The middle is elided with a clear marker.
func TestTruncateToolResult_PreservesHeadAndTail(t *testing.T) {
	head := "HEAD-MARKER-" + strings.Repeat("H", 4000)
	middle := strings.Repeat("M", 8000) // to be elided
	tail := strings.Repeat("T", 4000) + "-TAIL-MARKER"
	output := head + middle + tail

	const limit = 2000
	got := truncateToolResult(output, limit)

	if !strings.Contains(got, "HEAD-MARKER") {
		t.Error("truncated result should preserve the head")
	}
	if !strings.Contains(got, "TAIL-MARKER") {
		t.Error("truncated result should preserve the tail")
	}
	if !strings.Contains(got, "[goa-system] Tool result was truncated") {
		t.Errorf("truncated result should contain the elision marker, got %q", got)
	}
	if !strings.Contains(got, "elided") {
		t.Errorf("marker should indicate the middle was elided, got %q", got)
	}
	// The middle sentinel must be gone.
	if strings.Contains(got, "MMMM") {
		t.Error("the elided middle section leaked into the truncated result")
	}
	// And the result must be meaningfully smaller than the original.
	if len(got) >= len(output) {
		t.Errorf("result not smaller: %d >= %d", len(got), len(output))
	}
	if len(got) > limit*2 { // head + tail + marker, allow generous slack
		t.Errorf("result too large: %d (limit %d)", len(got), limit)
	}
}

// TestTruncateToolResult_NoTruncationUnderLimit verifies results at or under
// the limit pass through untouched.
func TestTruncateToolResult_NoTruncationUnderLimit(t *testing.T) {
	output := strings.Repeat("x", 500)
	if got := truncateToolResult(output, 1000); got != output {
		t.Errorf("output under limit should be unchanged, got len %d", len(got))
	}
}
