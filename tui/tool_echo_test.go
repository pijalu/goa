package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// The offscreen completion echo renders as a boxed one-liner in the tool's
// status color, keeping the ← continuation marker (bugs.md 2026-08-26).
func TestToolEcho_SuccessUsesGreenAndContinuationMarker(t *testing.T) {
	echo := newToolEcho("✓ $ ls -la — Took 0.08s · 2.4 KB · 44 lines", true)
	lines := echo.Render(80)
	if len(lines) != 1 {
		t.Fatalf("Render produced %d lines, want 1", len(lines))
	}
	joined := lines[0]
	if !strings.Contains(joined, ansi.Fg(TheTheme.ColorHex("tool_success"))) {
		t.Error("success echo not styled with tool_success color")
	}
	if strings.Contains(joined, TheTheme.ColorHex("tool_error")) {
		t.Error("success echo must not carry the error color")
	}
	if !strings.Contains(joined, "←") {
		t.Errorf("echo missing ← continuation marker: %q", ansi.Strip(joined))
	}
	if !strings.Contains(ansi.Strip(joined), "$ ls -la") {
		t.Errorf("echo lost its content: %q", ansi.Strip(joined))
	}
}

// Failed tools render with the error color so the echo reads as the tail of
// a red block.
func TestToolEcho_ErrorUsesRed(t *testing.T) {
	echo := newToolEcho("✗ bash exited 1 — Took 1.20s", false)
	line := echo.Render(80)[0]
	if !strings.Contains(line, ansi.Fg(TheTheme.ColorHex("tool_error"))) {
		t.Error("error echo not styled with tool_error color")
	}
	if strings.Contains(line, TheTheme.ColorHex("tool_success")) {
		t.Error("error echo must not carry the success color")
	}
}

// The echo is boxed: vertical borders frame the row (padding after the
// closing border is width fill, not content).
func TestToolEcho_Boxed(t *testing.T) {
	echo := newToolEcho("✓ probe", true)
	clean := strings.TrimRight(ansi.Strip(echo.Render(60)[0]), " ")
	if !strings.HasPrefix(clean, "│") || !strings.HasSuffix(clean, "│") {
		t.Errorf("echo not boxed with vertical borders: %q", clean)
	}
	if got := strings.Trim(clean, "│ "); got != "← ✓ probe" {
		t.Errorf("boxed content = %q, want %q", got, "← ✓ probe")
	}
}

// Long echoes truncate inside the box and stay within the terminal width.
func TestToolEcho_TruncatesToWidth(t *testing.T) {
	long := "✓ " + strings.Repeat("x", 200)
	line := ansi.Strip(newToolEcho(long, true).Render(40)[0])
	if got := len([]rune(line)); got != 40 {
		t.Errorf("rendered width = %d runes, want exactly 40 (padded)", got)
	}
	if !strings.Contains(line, "…") {
		t.Error("truncated echo should end with an ellipsis")
	}
}
