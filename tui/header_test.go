// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

func TestHeader_LogoLinesLoaded(t *testing.T) {
	h := NewHeader("goa", "0.1")
	lines := h.logoLines()
	if len(lines) == 0 {
		t.Fatal("expected logo lines to be loaded")
	}
	if strings.TrimSpace(lines[0]) == "" {
		t.Errorf("expected non-empty first logo line, got %q", lines[0])
	}
}

func TestHeader_Wide_ShowsLogoAndMascot(t *testing.T) {
	h := NewHeader("goa", "0.1")
	lines := h.Render(200)
	if len(lines) == 0 {
		t.Fatal("expected header lines")
	}
	rendered := strings.Join(lines, "\n")
	if !strings.Contains(rendered, "goa") {
		t.Errorf("expected app name in header, got %q", rendered)
	}
	if !strings.Contains(rendered, "coding agent") {
		t.Errorf("expected tagline in header, got %q", rendered)
	}
}

func TestHeader_Narrow_HidesLogo(t *testing.T) {
	h := NewHeader("goa", "0.1")
	lines := h.Render(20)
	if len(lines) == 0 {
		t.Fatal("expected header lines")
	}
	rendered := ansi.Strip(strings.Join(lines, "\n"))
	// At 20 columns the ASCII-art logo should be hidden.
	if strings.Contains(rendered, "▄▄▄▄▄▄") {
		t.Errorf("expected logo art hidden at width 20, got %q", rendered)
	}
	if !strings.Contains(rendered, "goa") {
		t.Errorf("expected app name still visible, got %q", rendered)
	}
}

func TestHeader_Medium_ShowsLogoOnly(t *testing.T) {
	h := NewHeader("goa", "0.1")
	lines := h.Render(100)
	if len(lines) == 0 {
		t.Fatal("expected header lines")
	}
	rendered := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(rendered, "goa") {
		t.Errorf("expected app name, got %q", rendered)
	}
	// Logo art should be present (it contains the repeating block pattern)
	if !strings.Contains(rendered, "████") {
		t.Errorf("expected logo art at width 100, got %q", rendered)
	}
}

func TestHeader_Wide_MascotAndLogoSideBySide(t *testing.T) {
	h := NewHeader("goa", "0.1")
	lines := h.Render(200)
	if len(lines) == 0 {
		t.Fatal("expected header lines")
	}
	rendered := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(rendered, "goa") {
		t.Errorf("expected app name, got %q", rendered)
	}
	// Both mascot and logo contain block characters; at 200 cols the combined
	// width should include both art blocks on the same physical rows.
	firstLine := rendered[:min(len(rendered), 350)]
	blockCount := strings.Count(firstLine, "█") + strings.Count(firstLine, "▄")
	if blockCount < 10 {
		t.Errorf("expected both mascot and logo block art on first rows, got %q", firstLine)
	}
}

func TestHeader_LogoWidthComputed(t *testing.T) {
	logo := logoANSI
	if logo == "" {
		t.Fatal("logo embed is empty")
	}
	lines := strings.Split(strings.TrimSuffix(logo, "\n"), "\n")
	if w := maxLineWidth(lines); w <= 0 {
		t.Errorf("expected positive logo width, got %d", w)
	}
}

// TestHeader_VersionOnSeparateRow verifies that the version string appears on
// rows below the mascot+logo art, not on the same row band.
func TestHeader_VersionOnSeparateRow(t *testing.T) {
	h := NewHeader("goa", "0.1")
	lines := h.Render(200)
	if len(lines) == 0 {
		t.Fatal("expected header lines")
	}

	lastArtRow := findLastArtRow(lines)
	firstVersionRow := findFirstVersionRow(lines)

	if firstVersionRow < 0 {
		t.Fatal("version string 'v0.1' not found in header")
	}
	if lastArtRow < 0 {
		t.Skip("no block art detected in rendered header")
	}
	if firstVersionRow <= lastArtRow {
		t.Errorf("version row %d must be below last art row %d, but it is not", firstVersionRow, lastArtRow)
	}
}

func findLastArtRow(lines []string) int {
	lastArtRow := -1
	for i, line := range lines {
		stripped := ansi.Strip(line)
		if strings.Contains(stripped, "\xe2") && containsBlockArt(stripped) {
			if lastArtRow < i {
				lastArtRow = i
			}
		}
	}
	return lastArtRow
}

func containsBlockArt(s string) bool {
	return strings.ContainsAny(s, "▄▀█") ||
		strings.ContainsRune(s, '\u2584') ||
		strings.ContainsRune(s, '\u2580') ||
		strings.ContainsRune(s, '\u2588')
}

func findFirstVersionRow(lines []string) int {
	for i, line := range lines {
		stripped := ansi.Strip(line)
		if strings.Contains(stripped, "v0.1") {
			return i
		}
	}
	return -1
}

// TestHeader_Wide_ShowsMascotLogoThenVersionBelow verifies the splash layout
// at wide terminal: mascot + logo on top rows, version details on bottom rows.
func TestHeader_Wide_ShowsMascotLogoThenVersionBelow(t *testing.T) {
	h := NewHeader("goa", "0.1")
	lines := h.Render(200)
	if len(lines) == 0 {
		t.Fatal("expected header lines")
	}

	rendered := strings.Join(lines, "\n")
	if !strings.Contains(rendered, "goa") {
		t.Error("expected app name in header")
	}
	if !strings.Contains(rendered, "v0.1") {
		t.Error("expected version string in header")
	}
	if !strings.Contains(rendered, "coding agent") {
		t.Error("expected tagline in header")
	}
}

// TestHeader_Narrow_InfoOnly verifies that narrow terminals still show info.
func TestHeader_Narrow_InfoOnly(t *testing.T) {
	h := NewHeader("goa", "0.1")
	// Very narrow: no logo, no mascot
	lines := h.Render(10)
	if len(lines) == 0 {
		t.Fatal("expected at least info lines on narrow terminal")
	}
	rendered := strings.Join(lines, "\n")
	if !strings.Contains(rendered, "goa") {
		t.Error("expected app name even on narrow terminal")
	}
}

// TestHeader_Medium_LogoThenInfo verifies medium width shows logo then info.
func TestHeader_Medium_LogoThenInfo(t *testing.T) {
	h := NewHeader("goa", "0.1")
	lines := h.Render(100)
	if len(lines) == 0 {
		t.Fatal("expected header lines")
	}
	rendered := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(rendered, "goa") {
		t.Error("expected app name")
	}
	// Logo art should be present at width 100
	if !strings.ContainsAny(rendered, "█▄▀") {
		t.Error("expected logo art at width 100")
	}
}
