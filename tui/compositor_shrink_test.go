package tui

import (
	"strings"
	"testing"
)

func assertShrinkRowsUnique(t *testing.T, emu *TermEmulator) {
	t.Helper()
	all := append([]string{}, emu.Scrollback()...)
	for row := 0; row < 10; row++ {
		all = append(all, emu.Visible(row))
	}
	for i := 0; i < 21; i++ {
		want := "row-" + itoaStr(i)
		count := 0
		for _, line := range all {
			if strings.TrimSpace(line) == want {
				count++
			}
		}
		if count > 1 {
			t.Errorf("row %q appears %d times (want ≤1)\nscrollback:\n%s\nscreen:\n%s", want, count, strings.Join(emu.Scrollback(), "\n"), joinVisibleEmu(emu, 10))
		}
	}
}

func assertShrinkNewestVisible(t *testing.T, emu *TermEmulator) {
	t.Helper()
	for row := 0; row < 10; row++ {
		if strings.Contains(emu.Visible(row), "row-19") {
			return
		}
	}
	t.Errorf("newest content missing after 1-row shrink:\n%s", joinVisibleEmu(emu, 10))
}
