// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"os"
	"strings"
	"testing"
)

// frameDump records one frame's key landmarks for the replay analysis.
type frameDump struct {
	idx        int
	footerRow  int // 0-indexed screen row of the footer stats line, -1 if none
	footerHits int // how many rows matched the footer signature
	inputTop   int // 0-indexed screen row of the input editor top border (╭), -1 if none
	bottomFill []bool
}

// TestReplayFooterMovement replays a captured GOA_DEBUG_TERMINAL log through
// the faithful TermEmulator and tracks where the footer lands on screen frame
// by frame. It reports any frame where the footer's screen row changes, which
// is the direct signature of the "footer moving up/down during streaming"
// ghosting artifact.
func TestReplayFooterMovement(t *testing.T) {
	const logPath = "/tmp/goa-term-flash-2.log"
	const termH, termW = 29, 150

	writes := loadFlashLogWrites(t, logPath)
	emu := NewTermEmulator(termH, termW)
	isFooter := footerMatcher()

	frames := replayFrames(emu, writes, termH, isFooter)
	changes := analyzeFooterChanges(frames)

	logFooterDistribution(t, frames, termH)
	logFooterChanges(t, changes)
	logInputTopChanges(t, frames)
	dumpSelectedFrames(t, writes, termH, isFooter)
}

// loadFlashLogWrites reads and parses the flash log, skipping the test if the
// log file is not available.
func loadFlashLogWrites(t *testing.T, logPath string) []string {
	t.Helper()
	f, err := os.Open(logPath)
	if err != nil {
		t.Skipf("flash log not available: %v", err)
	}
	defer f.Close()

	writes, err := parseFlashLog(f)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	t.Logf("parsed %d writes, replaying at %dx%d", len(writes), 29, 150)
	return writes
}

// footerMatcher returns a function that recognises footer status rows.
func footerMatcher() func(string) bool {
	return func(s string) bool {
		return strings.Contains(s, "tok/s") ||
			strings.Contains(s, "(auto)") ||
			strings.Contains(s, "(lmstudio)") ||
			strings.Contains(s, "(google)") ||
			strings.Contains(s, "Answering")
	}
}

// replayFrames processes all writes and snapshots state after each synced frame.
func replayFrames(emu *TermEmulator, writes []string, termH int, isFooter func(string) bool) []frameDump {
	frames := []frameDump{snapshotFrame(emu, -1, termH, isFooter)}
	for wi, w := range writes {
		emu.Process(w)
		if strings.HasSuffix(w, "\x1b[?2026l") {
			frames = append(frames, snapshotFrame(emu, wi, termH, isFooter))
		}
	}
	return frames
}

// snapshotFrame captures a single frame's footer and input-top landmarks.
func snapshotFrame(emu *TermEmulator, idx, termH int, isFooter func(string) bool) frameDump {
	fr := frameDump{idx: idx, footerRow: -1, inputTop: -1, bottomFill: make([]bool, termH)}
	for r := termH - 1; r >= 0; r-- {
		row := strings.TrimSpace(emu.Visible(r))
		if isFooter(row) {
			if fr.footerRow < 0 {
				fr.footerRow = r
			}
			fr.footerHits++
		}
	}
	for r := termH - 1; r >= 0; r-- {
		if strings.Contains(emu.Visible(r), "╭") {
			fr.inputTop = r
			break
		}
	}
	for r := 0; r < termH; r++ {
		if strings.TrimSpace(emu.Visible(r)) != "" {
			fr.bottomFill[r] = true
		}
	}
	return fr
}

// change records a single footer row movement.
type change struct {
	from, to, at int
}

// analyzeFooterChanges builds a list of footer row changes from the frames.
func analyzeFooterChanges(frames []frameDump) []change {
	var changes []change
	prev := frames[0].footerRow
	for _, fr := range frames[1:] {
		if fr.footerRow != prev {
			changes = append(changes, change{from: prev, to: fr.footerRow, at: fr.idx})
			prev = fr.footerRow
		}
	}
	return changes
}

// logFooterDistribution prints how many frames the footer spent on each row.
func logFooterDistribution(t *testing.T, frames []frameDump, termH int) {
	t.Helper()
	rowCount := map[int]int{}
	for _, fr := range frames {
		rowCount[fr.footerRow]++
	}
	t.Logf("footer row distribution (row: frame-count):")
	for r := 0; r < termH; r++ {
		if rowCount[r] > 0 {
			t.Logf("  row %d -> %d frames", r, rowCount[r])
		}
	}
	t.Logf("footer row changed %d times across the session", len(analyzeFooterChanges(frames)))
}

// logFooterChanges prints the first 50 footer row changes.
func logFooterChanges(t *testing.T, changes []change) {
	t.Helper()
	limit := len(changes)
	if limit > 50 {
		limit = 50
	}
	for i := 0; i < limit; i++ {
		c := changes[i]
		t.Logf("  change #%d: footer row %d -> %d (write %d)", i+1, c.from, c.to, c.at)
	}
}

// logInputTopChanges prints how many times the input-top border moved.
func logInputTopChanges(t *testing.T, frames []frameDump) {
	t.Helper()
	inputChanges := 0
	prevIn := frames[0].inputTop
	for _, fr := range frames[1:] {
		if fr.inputTop >= 0 && prevIn >= 0 && fr.inputTop != prevIn {
			inputChanges++
		}
		if fr.inputTop >= 0 {
			prevIn = fr.inputTop
		}
	}
	t.Logf("input-top border moved %d times", inputChanges)
}

// dumpSelectedFrames replays the captured log up to hand-selected writes and
// prints the resulting screen for manual diagnosis.
func dumpSelectedFrames(t *testing.T, writes []string, termH int, isFooter func(string) bool) {
	t.Helper()
	manual := []int{914, 924, 933, 965, 514, 850}
	dumpTargets := map[int]bool{}
	for _, m := range manual {
		dumpTargets[m] = true
	}

	shown := 0
	for wi := range writes {
		if !dumpTargets[wi] || shown >= 6 {
			continue
		}
		e := replayUpTo(writes, termH, 150, wi)
		landed := snapshotOf(e, wi, termH, isFooter)
		t.Logf("=== write %d: footer at row %d (hits=%d) ===", wi, landed.footerRow, landed.footerHits)
		dumpFrame(t, e, landed, termH)
		shown++
		if shown >= 12 {
			break
		}
	}
}

// replayUpTo creates a fresh emulator and replays writes up to and including idx.
func replayUpTo(writes []string, h, w, idx int) *TermEmulator {
	e := NewTermEmulator(h, w)
	for j := 0; j <= idx; j++ {
		e.Process(writes[j])
	}
	return e
}

func snapshotOf(emu *TermEmulator, idx, termH int, isFooter func(string) bool) frameDump {
	fr := frameDump{idx: idx, footerRow: -1, inputTop: -1, bottomFill: make([]bool, termH)}
	for r := termH - 1; r >= 0; r-- {
		row := strings.TrimSpace(emu.Visible(r))
		if isFooter(row) {
			if fr.footerRow < 0 {
				fr.footerRow = r
			}
			fr.footerHits++
		}
	}
	for r := termH - 1; r >= 0; r-- {
		if strings.Contains(emu.Visible(r), "╭") {
			fr.inputTop = r
			break
		}
	}
	return fr
}

func dumpFrame(t *testing.T, emu *TermEmulator, fr frameDump, termH int) {
	t.Helper()
	for r := 0; r < termH; r++ {
		row := emu.Visible(r)
		trim := strings.TrimRight(row, " ")
		mark := ""
		if r == fr.footerRow {
			mark = "  <== FOOTER"
		}
		if r == fr.inputTop {
			mark += "  <== INPUT-TOP"
		}
		if len(trim) > 95 {
			trim = trim[:95]
		}
		t.Logf("  %2d|%s%s", r, trim, mark)
	}
}
