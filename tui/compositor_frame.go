// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

const (
	frameDiff          frameKind = iota // steady state: incremental diff
	frameFirst                          // very first frame
	frameGeometryReset                  // width or bottom-chrome height changed
	frameFullRepaint                    // height-only resize or overlay present
)

// classifyFrame computes the frame kind and updates c.chromeH, c.prevChromeH
// and scene.WidthChanged as a side effect (they describe the current and
// previous geometry).
func (c *Compositor) classifyFrame(scene *Scene, width, height int) frameKind {
	// Track the chrome-band height across frames: prevWindowFull consults
	// prevChromeH to map the PREVIOUS canvas's content end ("Slow
	// performance on very large conversations").
	c.prevChromeH = c.chromeH
	c.chromeH = max(scene.ChromeHeight, 0)

	widthChanged := c.prevW != 0 && c.prevW != width
	heightChanged := c.prevH != 0 && c.prevH != height
	scene.WidthChanged = widthChanged

	switch {
	case c.prevLines == nil:
		return frameFirst
	case widthChanged:
		// Only a WIDTH change invalidates the scrollback↔canvas
		// correspondence (line wrap reflows): reset + re-emit. A bottom-chrome
		// height change (editor newline, steering/goal bubble appearing or
		// clearing) does NOT: canvas rows are immutable, the watermark stays
		// valid, and the incremental diff handles the shift at O(viewport)
		// cost. Routing chrome changes through the geometry reset wiped
		// scrollback and re-emitted the ENTIRE transcript per keystroke —
		// the 100%-CPU-for-seconds hang on very large conversations.
		return frameGeometryReset
	case heightChanged || c.hasOverlay(scene):
		return frameFullRepaint
	default:
		return frameDiff
	}
}

// cullFloor returns the canvas row below which content may be left unwritten:
// a geometry reset re-emits the whole transcript (full canvas, 0); steady
// frames may cull rows already in scrollback (the watermark).
func (k frameKind) cullFloor(scrollTop int) int {
	if k == frameGeometryReset {
		return 0
	}
	return scrollTop
}

// hasOverlay reports whether the scene carries any overlay layer.
func (c *Compositor) hasOverlay(scene *Scene) bool {
	for _, l := range scene.Layers {
		if l.Kind == LayerOverlay {
			return true
		}
	}
	return false
}

// emitOverflow emits into scrollback every transcript row that has scrolled
// off the top since the watermark was last advanced, then advances the
// appendOverflow emits into buf every transcript row that has scrolled off the
// top since the watermark was last advanced, then advances the watermark. It is
// the single place the watermark moves, so a row is emitted exactly once and
// the chrome band is never crossed. The bytes are folded into the caller's
// already-open sync so the scroll and the subsequent window repaint commit
// atomically (no intermediate footer-less frame).
func (c *Compositor) appendOverflow(buf *strings.Builder, canvas []string, width, height int) {
	vt := max(0, len(canvas)-height)
	contentEnd := len(canvas) - c.chromeH
	if contentEnd < 0 {
		contentEnd = 0
	}
	target := vt
	if target > contentEnd {
		target = contentEnd
	}
	if target <= c.scrollTop {
		return // nothing new scrolled off; watermark never moves backward
	}
	// Advance from the row currently at the top of the screen (c.vt) to the new
	// top. Rows before c.vt are already in scrollback (watermark c.scrollTop).
	from := c.vt
	if from < c.scrollTop {
		from = c.scrollTop
	}
	c.emitScrollbackAdvance(buf, canvas, from, target, width, height)
	c.scrollTop = target
}

// deferScrollbackSync handles a mid-transcript growth above the visible
// window without re-emitting the whole transcript. The grown rows belong in
// terminal scrollback, but a terminal cannot insert rows into the middle of
// its scrollback — the only exact sync would wipe and re-emit everything
// (O(transcript) per chunk, the CPU storm). The screen content is unchanged
// (the growth is above the window), so this frame needs no terminal write for
// the transcript: advance the watermark bookkeeping to the natural target and
// repaint the (identical) window cheaply. The caller keeps scrollbackDirty set
// so a later settled frame performs ONE full reset to re-sync the scrollback.
//
// drawWindow skips its scrollback overflow because scrollTop already equals
// the frame's target; it still repaints the window and chrome (bounded by the
// terminal height, not the transcript length).
func (c *Compositor) deferScrollbackSync(canvas []string, cursor *CursorPos, width, height, target int) {
	c.scrollbackDirty = true
	c.scrollTop = target
	c.drawWindow(canvas, cursor, width, height, false)
}

// handleMidTranscriptEdit is the guard for the incremental paths: when content
// above the new window top changed identity since the previous frame (a
// streaming block growing ABOVE later-appended content, e.g. /quota or a
// screen-filling /goal:list landing mid-stream), the incremental scroll
// emission would scroll wrong row identities into scrollback and skip the real
// ones.
//
// The previous response was a full scrollback reset (drawWindowResetScrollback)
// on every hit — O(transcript) per stream chunk (each row re-emitted through
// uniseg width truncation): the CPU >100% storm on long sessions, and the
// repeated \x1b[3J wipes that yanked the terminal viewport (the reported
// jump-back / scroll-back-down during /goal:list while streaming). This
// replaces it with a defer-and-sync strategy:
//
//   - Buried growth (canvas grew, window byte-identical): DEFER the scrollback
//     sync. The screen is unchanged, so we advance the watermark bookkeeping
//     and repaint the (identical) window cheaply (O(viewport)); scrollbackDirty
//     records that the terminal scrollback diverged (the grown rows cannot be
//     inserted mid-scrollback).
//   - Any other mid-transcript instability that CHANGED the window (e.g. a
//     large bottom-append with displaced chrome rows): the window must be
//     rebuilt and the scrollback re-emitted for the new layout — the full reset
//     is correct and necessary here.
//   - While scrollbackDirty: if the conversation settled (Scene.MutationGen
//     unchanged — the stream stopped mutating) OR the visible window changed
//     (a new block started after the appended content), ONE full reset
//     re-syncs the stale scrollback before any further incremental scroll
//     emission. A buried in-place edit (no height growth, window unchanged)
//     falls through: the normal diff path no-ops the unchanged window and does
//     not emit scrollback, so the stale state is preserved for the later sync.
//
// Returns true when the frame was fully handled (the caller must return).
func (c *Compositor) handleMidTranscriptEdit(scene *Scene, canvas []string, width, height int, kind frameKind, clearPending bool) bool {
	if kind != frameDiff && kind != frameFullRepaint {
		return false
	}
	vt := c.windowTop(len(canvas), height)
	target := c.scrollTarget(vt, len(canvas))
	if c.growthAboveWindow(canvas, height) {
		if clearPending {
			// A pending Clear wipes the whole screen+scrollback this frame
			// anyway; the reset path performs it atomically.
			canvas, _ = scene.compose(0)
			c.drawWindowResetScrollback(canvas, scene.Cursor, width, height)
			c.scrollbackDirty = false
		} else {
			c.deferScrollbackSync(canvas, scene.Cursor, width, height, target)
		}
		c.prevMutationGen = scene.MutationGen
		c.prevLines = copySlice(canvas)
		c.prevW = width
		c.prevH = height
		return true
	}
	if c.scrollOffUnstable(canvas, target) {
		// Mid-transcript edit that also CHANGED the window (e.g. a large
		// bottom-append whose scroll-off region includes displaced chrome rows):
		// the window must be rebuilt and the scrollback re-emitted for the new
		// layout. The per-chunk full reset is correct here — the screen
		// genuinely changed, so there is no cheaper path.
		canvas, _ = scene.compose(0)
		c.drawWindowResetScrollback(canvas, scene.Cursor, width, height)
		c.scrollbackDirty = false
		c.prevMutationGen = scene.MutationGen
		c.prevLines = copySlice(canvas)
		c.prevW = width
		c.prevH = height
		return true
	}
	if c.scrollbackDirty && (scene.MutationGen == c.prevMutationGen || !c.windowUnchanged(canvas, height)) {
		// Either the conversation settled (no mutation since the last frame) or
		// the visible window changed (a new block started after the appended
		// content): the stale scrollback must be re-synced with ONE full reset
		// before any further incremental scroll emission, or the wrong rows
		// would be pushed into scrollback.
		canvas, _ = scene.compose(0)
		c.drawWindowResetScrollback(canvas, scene.Cursor, width, height)
		c.scrollbackDirty = false
		c.prevMutationGen = scene.MutationGen
		c.prevLines = copySlice(canvas)
		c.prevW = width
		c.prevH = height
		return true
	}
	// A buried in-place edit (no height growth, window unchanged) falls through:
	// the normal diff path no-ops the unchanged window and does not emit
	// scrollback, so the stale scrollback is preserved for the later sync.
	return false
}

// drawWindow redraws the whole visible window top-down with absolute CUP in
// one synchronized frame. It first emits any newly scrolled-off rows into
// scrollback (via appendOverflow), then repaints every visible row (CUP +
// \x1b[2K + content). It never wipes the screen first: the repaint loop
// already clears and rewrites EVERY row of the window, so a preceding
// full-screen wipe (\x1b[2J, removed 2026-07-21) was redundant — and
// harmful: on real terminals it visibly blanks the screen before the
// rewrite lands (even inside CSI 2026 on several emulators), which produced
// the black flash / mascot-flicker seen on overlay frames during tool calls
// (Mascot/logo redraw, HIGH). In-place row replacement — the same
// strategy renderDiff uses — is flicker-free and leaves no stale content:
// every row is overwritten, and a shorter canvas shifts the window bottom up
// rather than leaving residue.
func (c *Compositor) drawWindow(canvas []string, cursor *CursorPos, width, height int, wipe bool) {
	c.setTracePath("full")
	c.fullRedrawCount++
	vt := c.windowTop(len(canvas), height)

	var buf strings.Builder
	buf.WriteString("\x1b[?2026h")
	if wipe {
		// A Clear() requested a screen+scrollback wipe. Fold it into THIS frame's
		// sync (instead of a separate immediate write) so it commits atomically
		// with the repaint — a stale pre-clear frame can never be painted on top.
		buf.WriteString("\x1b[2J\x1b[H\x1b[3J")
	}
	c.appendOverflow(&buf, canvas, width, height)
	// drawWindow repaints every row (first frame / full repaint), so the scroll
	// advance did not pre-write any bottom rows for repaintWindow to skip.
	c.lastScrollCount = 0
	// Two-phase repaint: transcript in [1, windowH], chrome in [windowH+1, height].
	// This keeps the chrome band pinned at the screen bottom even when vt is
	// clamped above the natural anchor (windowTop's watermark clamp).
	windowH := height - c.chromeH
	if windowH < 1 {
		windowH = 1
	}
	contentEnd := len(canvas) - c.chromeH
	if contentEnd < 0 {
		contentEnd = 0
	}
	transcriptEnd := vt + windowH
	if transcriptEnd > contentEnd {
		transcriptEnd = contentEnd
	}
	for i := vt; i < transcriptEnd; i++ {
		screenRow := i - vt + 1
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[2K", screenRow))
		buf.WriteString(truncateToWidth(canvas[i], width, ""))
		c.traceWroteRow(screenRow)
	}
	// Clear any stale transcript rows between the transcript end and the
	// chrome band (a clamped window can leave rows the taller previous frame
	// painted).
	for screenRow := transcriptEnd - vt + 1; screenRow <= windowH; screenRow++ {
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[2K", screenRow))
		c.traceWroteRow(screenRow)
	}
	// Draw the pinned chrome band in the rows below the transcript region.
	for i := contentEnd; i < len(canvas); i++ {
		screenRow := windowH + (i - contentEnd) + 1
		if screenRow > height {
			break
		}
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[2K", screenRow))
		buf.WriteString(truncateToWidth(canvas[i], width, ""))
		c.traceWroteRow(screenRow)
	}
	c.appendCursorSeq(&buf, cursor, len(canvas), width, vt, height)
	buf.WriteString("\x1b[?2026l")
	c.terminal.Write([]byte(buf.String()))

	c.vt = vt
	c.cursorRow = max(0, len(canvas)-1)
	c.hardwareCursorRow = max(0, len(canvas)-1)
}

// drawWindowResetScrollback handles a terminal WIDTH change. At a new width
// the line wrap reflows, so the rows already sitting in the terminal's
// scrollback (emitted at the old width) no longer correspond to the canvas —
// leaving them produces a stale, misaligned history. This clears the
// scrollback (\x1b[3J), resets the watermark, re-emits every off-screen
// transcript row at the new width, then repaints the visible window. The
// result is a scrollback that matches the new layout exactly, as if the app
// had been rendered at this width all along.
func (c *Compositor) drawWindowResetScrollback(canvas []string, cursor *CursorPos, width, height int) {
	c.setTracePath("full-reset")
	c.fullRedrawCount++
	vt := max(0, len(canvas)-height)

	var buf strings.Builder
	buf.WriteString("\x1b[?2026h")
	// Wipe the visible screen AND the scrollback so no old-width rows survive.
	buf.WriteString("\x1b[2J\x1b[H\x1b[3J")
	// Reset the watermark so every off-screen row is re-emitted at the new
	// width (appendOverflow would otherwise treat them as already-scrolled).
	c.scrollTop = 0
	c.vt = 0
	c.reemitScrollback(&buf, canvas, vt, width, height)
	// Repaint the visible window. The transcript occupies only the top
	// height-chromeH rows (the chrome band is pinned below), so the window is
	// windowH rows tall, not height. Repainting canvas[vt:] (height rows) would
	// overflow chromeH rows into the chrome zone and duplicate content — the
	// reset-path duplicate-row bug. Stop the transcript repaint at windowH,
	// then draw the chrome band in its own rows below.
	windowH := height - c.chromeH
	if windowH < 1 {
		windowH = 1
	}
	transcriptEnd := vt + windowH
	if transcriptEnd > len(canvas)-c.chromeH {
		transcriptEnd = len(canvas) - c.chromeH
	}
	for i := vt; i < transcriptEnd; i++ {
		screenRow := i - vt + 1
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[2K", screenRow))
		buf.WriteString(truncateToWidth(canvas[i], width, ""))
		c.traceWroteRow(screenRow)
	}
	// Draw the pinned chrome band in the rows below the transcript region.
	for i := transcriptEnd; i < len(canvas); i++ {
		screenRow := windowH + (i - transcriptEnd) + 1
		if screenRow > height {
			break
		}
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[2K", screenRow))
		buf.WriteString(truncateToWidth(canvas[i], width, ""))
		c.traceWroteRow(screenRow)
	}
	c.appendCursorSeq(&buf, cursor, len(canvas), width, vt, height)
	buf.WriteString("\x1b[?2026l")
	c.terminal.Write([]byte(buf.String()))

	c.scrollTop = vt // everything above the window is now in scrollback
	c.vt = vt
	c.cursorRow = max(0, len(canvas)-1)
	c.hardwareCursorRow = max(0, len(canvas)-1)
}

// reemitScrollback writes every transcript row above the window (rows
// [0, vt)) into scrollback at the current width, top-down, after a scrollback
// reset. It mirrors emitFirstFrameScroll: writing the full transcript and
// letting line-feeds scroll the top rows off leaves exactly [0, vt) in
// scrollback and [vt, vt+windowH) on screen.
func (c *Compositor) reemitScrollback(buf *strings.Builder, canvas []string, vt, width, height int) {
	windowH, _ := c.transcriptWindow(buf, height)
	contentEnd := max(0, len(canvas)-c.chromeH)
	c.emitFirstFrameScroll(buf, canvas, vt, windowH, contentEnd, width)
	if c.chromeH > 0 {
		c.resetScrollRegion(buf)
	}
}

// renderDiff is the steady-state path: emit newly scrolled-off rows, then
// repaint only the changed rows of the visible window.
func (c *Compositor) renderDiff(canvas []string, cursor *CursorPos, width, height int) {
	c.setTracePath("diff")
	// The window top is clamped to the scrollback watermark: when the canvas
	// transiently shrinks below scrollTop (e.g. a thinking block finalized
	// into a shorter entry for one frame), the window stays anchored at the
	// rows the terminal actually shows instead of following vt below the
	// watermark and repainting already-scrolled rows (the mascot flash).
	vt := c.windowTop(len(canvas), height)
	target := c.scrollTarget(vt, len(canvas))

	var buf strings.Builder
	buf.WriteString("\x1b[?2026h")
	c.advanceScrollback(&buf, canvas, target, width, height)
	// repaintWindow must not redraw the rows the scroll just revealed at the
	// bottom of the window. The count must mirror what advanceScrollback
	// actually wrote — (target - max(c.vt, c.scrollTop)) — NOT (target - c.vt):
	// when the window top moved but the watermark did NOT advance (canvas
	// shrank then regrew, e.g. a stream finalizing mid-flip), the scroll wrote
	// ZERO rows, and counting (target - c.vt) skips a bottom transcript row
	// that was never repainted — leaving a stale row behind (Bug D:
	// a tool widget's last line kept the pending bg after success).
	c.lastScrollCount = max(0, target-max(c.vt, c.scrollTop))
	c.repaintWindow(&buf, canvas, vt, width, height)
	c.appendCursorSeq(&buf, cursor, len(canvas), width, vt, height)
	buf.WriteString("\x1b[?2026l")
	c.terminal.Write([]byte(buf.String()))
	c.lastScrollCount = 0

	c.scrollTop = target
	c.vt = vt
	c.cursorRow = max(0, len(canvas)-1)
	c.hardwareCursorRow = max(0, len(canvas)-1)
}

// windowTop computes the viewport top for a canvas of canvasLen rows on a
// terminal of `height` rows, reconciling the natural bottom anchor with the
// scrollback watermark.
//
// The window top is ALWAYS clamped to the scrollback watermark: rows
// [0, scrollTop) have been emitted into terminal scrollback exactly once and
// the terminal offers no "unscroll" — repainting them onto the visible window
// would duplicate them (the screen-glitching bug at the scrollback boundary).
//
// When the natural anchor dips below the watermark — a mid-transcript shrink
// (a thinking block finalized shorter) or a chrome-band shrink (goal bubble
// cleared) — the clamped window shows blank rows at the top of the
// transcript region. repaintWindow's two-phase layout keeps the chrome band
// pinned at the screen bottom regardless, so the blanks are truthful empty
// space, never displaced chrome.
//
// A deliberate transcript reset (/new, session switch) must call Clear first
// to zero the watermark; otherwise a from-scratch canvas would be anchored at
// a stale watermark instead of rendering fully.
func (c *Compositor) windowTop(canvasLen, height int) int {
	vt := max(0, canvasLen-height)
	if vt < c.scrollTop {
		vt = c.scrollTop
	}
	return vt
}

// scrollTarget computes the new scrollback watermark: the viewport top clamped
// to the transcript (never into the chrome band) and never moved backward.
// It also records the scroll in the frame trace.
func (c *Compositor) scrollTarget(vt, canvasLen int) int {
	contentEnd := canvasLen - c.chromeH
	if contentEnd < 0 {
		contentEnd = 0
	}
	target := min(vt, contentEnd)
	target = max(target, c.scrollTop)
	if c.curTrace != nil {
		c.curTrace.PrevVtop = c.vt
		c.curTrace.NewVtop = vt
		if target > c.scrollTop {
			c.curTrace.Scrolled = true
			c.curTrace.Scroll = target - c.scrollTop
		}
	}
	return target
}

// scrollOffUnstable reports whether the rows about to scroll into scrollback
// (canvas[from, to)) changed identity since the previous frame. The
// incremental scroll emission is only sound when that region is stable: both
// emission paths either push the screen's current top rows (which must equal
// the new canvas rows there) or write the new canvas rows and scroll the
// first of them (which must equal the screen rows). A MID-TRANSCRIPT edit
// above the window — a streaming block that grows ABOVE later-appended
// content, e.g. /quota landing mid-stream — shifts the region and the
// emission would destroy unscrolled rows and emit shifted duplicates
// (Slow performance on very large conversations: the quota-stream
// regression only stayed hidden because every chrome change force-resynced).
//
// Only MALIGNANT instability counts: a row whose content MOVED to a
// different canvas index (a position shift, e.g. a mid-transcript block
// inserted or collapsed above the scroll-off region). A row edited in place
// — its previous content no longer exists anywhere in the transcript, as
// with a live tool widget's ticking elapsed-time text — is benign: the
// physical row that scrolls off carries the right identity (one tick
// stale), and resetting scrollback for it would wipe and re-emit the whole
// transcript on every animation tick (the reset storm). Blank↔content
// transitions are benign too — the bottom-align pad rows being consumed by
// growth, or blank spacers — and the top-down path already handles them.
func (c *Compositor) scrollOffUnstable(canvas []string, to int) bool {
	if c.prevLines == nil {
		return false
	}
	from := max(c.vt, c.scrollTop)
	if from < 0 {
		from = 0
	}
	// Only compare within the PREVIOUS transcript: indices at/above
	// prevContentEnd held the previous chrome band (displaced by any
	// transcript growth) or did not exist yet — both benign.
	prevContentEnd := len(c.prevLines) - c.prevChromeH
	curIndex := map[string]int{} // lazily filled on the first in-region diff
	for i := from; i < to && i < len(canvas); i++ {
		if c.scrollRowShifted(canvas, i, prevContentEnd, &curIndex) {
			return true
		}
	}
	return false
}

// scrollRowShifted reports whether canvas row i holds previous content that
// MOVED to a different canvas index (malignant position shift). In-place
// edits and blank↔content transitions are benign (see scrollOffUnstable).
func (c *Compositor) scrollRowShifted(canvas []string, i, prevContentEnd int, curIndex *map[string]int) bool {
	if i < 0 || i >= prevContentEnd {
		return false
	}
	prev := strings.TrimSpace(ansi.Strip(c.prevLines[i]))
	cur := strings.TrimSpace(ansi.Strip(canvas[i]))
	if prev == "" || cur == "" || prev == cur {
		return false
	}
	if len(*curIndex) == 0 {
		*curIndex = indexCanvasRows(canvas, len(canvas)-c.chromeH)
	}
	j, ok := (*curIndex)[prev]
	return ok && j != i // position shift: incremental scroll would mis-emit
}

// growthAboveWindow reports whether the canvas grew while the visible window
// content stayed byte-identical — the signature of a streaming block growing
// entirely ABOVE the window (e.g. a buried stream under a screen-filling
// /goal:list). The incremental scroll is unsound for this even when the
// shifted rows are byte-identical (goal-list spacer rows): the steady path
// would scroll the window's top rows into scrollback although they must stay
// on screen. This is the same mid-transcript-edit condition scrollOffUnstable
// detects, but it catches the byte-identical-blank case the row-content
// comparison cannot see.
func (c *Compositor) growthAboveWindow(canvas []string, height int) bool {
	return len(canvas) > len(c.prevLines) && c.windowUnchanged(canvas, height)
}

// windowUnchanged reports whether the visible window content (the last
// transcript rows plus the chrome band) is byte-identical to the previous
// frame's window. Used to distinguish a buried mid-transcript edit (stream
// growing above the window) — where the screen needs no repaint and the
// incremental scroll must not run — from a genuine window change.
func (c *Compositor) windowUnchanged(canvas []string, height int) bool {
	if c.prevLines == nil {
		return false
	}
	// A chrome-band height change alters the window layout (the chrome rows
	// themselves), so the window is never "unchanged" across it even when the
	// transcript rows kept their content.
	if c.chromeH != c.prevChromeH {
		return false
	}
	contentEnd := len(canvas) - c.chromeH
	prevContentEnd := len(c.prevLines) - c.prevChromeH
	windowH := height - c.chromeH
	if windowH < 1 || contentEnd < windowH || prevContentEnd < windowH {
		return false
	}
	for i := 0; i < windowH; i++ {
		if canvas[contentEnd-windowH+i] != c.prevLines[prevContentEnd-windowH+i] {
			return false
		}
	}
	return true
}

// windowContentShifted reports whether any row of the visible window
// [vt, vt+height) moved POSITION since the previous frame — i.e. the window's
// content is not a pure scroll of the previous canvas but has an internal
// insertion/deletion (a tool widget growing or shrinking mid-window). In that
// case repaintWindow's unchangedRow skip — which maps screen rows to the
// previous canvas by the pure-scroll delta — is unsound and would leave stale
// and duplicated rows, so the caller must repaint the whole window instead.
//
// Like scrollOffUnstable, only a POSITION shift counts: an in-place edit (a
// live widget's ticking elapsed text) is benign — repaintWindow repaints that
// row because its bytes differ at the SAME index. Blank rows and rows whose
// previous content was in the (now displaced) chrome band are benign too. A
// pure bottom-append stream does NOT trip the guard: the existing window rows
// keep their pure-scroll indices (prev == cur), and the newly revealed bottom
// rows map to indices that held chrome/blank last frame.
func (c *Compositor) windowContentShifted(canvas []string, vt, height int) bool {
	if c.prevLines == nil {
		return false
	}
	// Only an insertion/deletion can shift a window row's canvas index; an
	// in-place edit (no length change — a ticking widget, a same-height text
	// update) leaves every row at its index and is handled correctly by the
	// diff repaint. Bail out unless the transcript grew or shrank so the
	// common in-place streaming/animation case is not misrouted to a full
	// repaint.
	if len(canvas)-c.chromeH == len(c.prevLines)-c.prevChromeH {
		return false
	}
	prevContentEnd := len(c.prevLines) - c.prevChromeH
	windowBottom := min(vt+height, len(canvas)-c.chromeH)
	curIndex := map[string]int{} // lazily filled on the first in-window diff
	for i := vt; i < windowBottom; i++ {
		if i >= prevContentEnd {
			break // at/above the old content end there was chrome or nothing
		}
		// Same-index comparison: under a pure scroll+append the transcript rows
		// keep their canvas indices (new rows append at the bottom), so canvas[i]
		// still holds prevLines[i]. A tool widget growing/shrinking INSIDE the
		// window inserts/deletes rows and shifts every row below it, so canvas[i]
		// no longer matches prevLines[i] there — the exact condition that breaks
		// repaintWindow's unchangedRow skip and leaves duplicate/lost rows around
		// the history↔screen boundary (the reported tool-call duplicate bug).
		prev := strings.TrimSpace(ansi.Strip(c.prevLines[i]))
		cur := strings.TrimSpace(ansi.Strip(canvas[i]))
		if prev == "" || cur == "" || prev == cur {
			continue
		}
		if len(curIndex) == 0 {
			curIndex = indexCanvasRows(canvas, len(canvas)-c.chromeH)
		}
		// The previous content still exists in the transcript but MOVED to a
		// different index (j != i): an internal insertion/deletion shifted it,
		// so the window is not a pure scroll and the diff repaint is unsound.
		// (If it no longer exists anywhere, it was an in-place edit — benign:
		// repaintWindow repaints that row because its bytes differ at index i.)
		if j, ok := curIndex[prev]; ok && j != i {
			return true
		}
	}
	return false
}

// indexCanvasRows maps each transcript row's normalized content (ANSI
// stripped, whitespace trimmed) to its canvas index, excluding the chrome
// band. Rows past contentEnd are chrome, not transcript, so they never
// participate in shift detection. Duplicate rows collapse to one index —
// acceptable, since identical rows scrolling are visually indistinguishable.
func indexCanvasRows(canvas []string, contentEnd int) map[string]int {
	if contentEnd < 0 || contentEnd > len(canvas) {
		contentEnd = len(canvas)
	}
	idx := make(map[string]int, contentEnd)
	for i := 0; i < contentEnd; i++ {
		idx[strings.TrimSpace(ansi.Strip(canvas[i]))] = i
	}
	return idx
}

// advanceScrollback emits the rows that scrolled off since the last frame into
// terminal scrollback, exactly once each. When chrome is pinned the scroll is
// confined to the transcript region via DECSTBM.
