// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/review"
)

const reviewScrollDiff = `diff --git a/bugs.md b/bugs.md
--- a/bugs.md
+++ b/bugs.md
@@ -29,4 +29,16 @@ If stuck, and only if stuck, you can refer to the pi, kimi-code, or opencode pro
 6. Verify against the original failing command before declaring done.
 7. Run the code-quality checks from guideline #6 separately and confirm the fix does not introduce new violations.
 8. Extra line A.
 9. Extra line B.
 10. Extra line C.
`

func setupReviewScrollTest(t *testing.T, baseCount int) (*TUI, *fakeTerminal, func()) {
	t.Helper()
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	cv := NewChatViewport()
	for i := 0; i < baseCount; i++ {
		cv.AddSystemMessage(fmt.Sprintf("chat line %02d", i))
	}
	engine.AddChild(cv)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	pager := NewReviewPager(s, reviewScrollDiff)
	pager.SetViewport(term.w, 19)
	engine.ShowOverlay(pager, OverlayOptions{Width: 0, Height: 19, BottomOffset: 5, CaptureInput: true})
	engine.RenderNow()
	return engine, term, func() { engine.Stop() }
}

func visibleScreen(buf []string, h int) []string {
	visibleTop := max(0, len(buf)-h)
	screen := make([]string, h)
	for i := 0; i < h; i++ {
		if visibleTop+i < len(buf) {
			screen[i] = stripReviewANSi(buf[visibleTop+i])
		}
	}
	return screen
}

func findReviewTitleRow(screen []string) int {
	for i, l := range screen {
		if strings.Contains(l, "Review abc12345") {
			return i
		}
	}
	return -1
}

func findSelectedLine7(screen []string) int {
	for i, l := range screen {
		if strings.HasPrefix(strings.TrimSpace(l), ">") && strings.Contains(l, "7.") {
			return i
		}
	}
	return -1
}

func assertNoDuplicateLines(t *testing.T, screen []string) {
	t.Helper()
	for i := 1; i < len(screen); i++ {
		a := strings.TrimSpace(screen[i-1])
		b := strings.TrimSpace(screen[i])
		if a != "" && a == b {
			t.Fatalf("duplicate screen rows %d/%d: %q; screen:\n%s", i-1, i, a, strings.Join(screen, "\n"))
		}
	}
}

func assertReviewScreenAfterDown(t *testing.T, screen []string) {
	t.Helper()
	if findReviewTitleRow(screen) != 0 {
		t.Fatalf("title row != 0; screen:\n%s", strings.Join(screen, "\n"))
	}
	selectedRow := findSelectedLine7(screen)
	if selectedRow < 0 {
		t.Fatalf("selected line 7 not found; screen:\n%s", strings.Join(screen, "\n"))
	}
	if selectedRow == 0 {
		t.Fatalf("selected row cannot be 0")
	}
	above := strings.TrimSpace(screen[selectedRow-1])
	if !strings.Contains(above, "6.") || strings.HasPrefix(above, ">") {
		t.Fatalf("row above selected line is not unselected line 6: %q; screen:\n%s", above, strings.Join(screen, "\n"))
	}
	assertNoDuplicateLines(t, screen)
}

func TestReviewScroll_PositionByBaseLength(t *testing.T) {
	for n := 0; n <= 60; n++ {
		n := n
		t.Run(fmt.Sprintf("base%d", n), func(t *testing.T) {
			engine, term, cleanup := setupReviewScrollTest(t, n)
			defer cleanup()

			term.onInput("down")
			engine.RenderNow()

			screen := visibleScreen(engine.Buffer(), term.h)
			assertReviewScreenAfterDown(t, screen)
		})
	}
}
