// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

func (c *Compositor) advanceScrollback(buf *strings.Builder, canvas []string, target, width, height int) {
	if target <= c.scrollTop {
		return
	}
	from := max(c.vt, c.scrollTop)
	c.emitScrollbackAdvance(buf, canvas, from, target, width, height)
}

// repaintWindow redraws the visible window with absolute CUP, skipping rows
// whose bytes are unchanged since the previous frame.
//
// The window is painted in two phases so the chrome band stays pinned at the
// screen bottom even when vt is clamped above the natural bottom anchor (a
// chrome-band shrink, where the watermark prevents revealing already-scrolled
// rows):
//
//  1. Transcript region: screen rows [1, windowH], mapping to canvas rows
//     [vt, vt+windowH). Rows past contentEnd render as blanks (the transcript
//     genuinely shrank or the watermark clamped above the natural anchor).
//  2. Chrome region: screen rows [windowH+1, height], mapping to canvas rows
//     [contentEnd, len(canvas)). The chrome band is always at the bottom of
//     the screen regardless of where vt lands.
//
// When the canvas is shorter than vt+windowH (it transiently shrank below the
// scrollback watermark), canvas indices past the transcript end render as
// blank rows — clearing the stale rows the taller previous frame left on the
// lower screen, without repainting the already-scrolled rows above vt.
func (c *Compositor) repaintWindow(buf *strings.Builder, canvas []string, vt, width, height int) {
	contentEnd := len(canvas) - c.chromeH
	windowH := height - c.chromeH
	if windowH < 1 {
		windowH = 1
	}
	skipFrom := contentEnd - c.lastScrollCount
	skipTo := contentEnd

	c.repaintTranscriptRows(buf, canvas, vt, windowH, contentEnd, skipFrom, skipTo, width)
	c.repaintChromeRows(buf, canvas, windowH, height, contentEnd, width)
}

// repaintTranscriptRows paints screen rows [1, windowH] from canvas transcript
// rows [vt, vt+windowH), skipping unchanged rows and the scroll-skip window.
func (c *Compositor) repaintTranscriptRows(buf *strings.Builder, canvas []string, vt, windowH, contentEnd, skipFrom, skipTo, width int) {
	for screenRow := 1; screenRow <= windowH; screenRow++ {
		i := vt + screenRow - 1
		line := ""
		if i >= 0 && i < contentEnd {
			line = canvas[i]
		}
		if c.lastScrollCount > 0 && i >= skipFrom && i < skipTo {
			continue
		}
		if c.unchangedRowTranscript(canvas, i, vt) {
			continue
		}
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[2K", screenRow))
		buf.WriteString(truncateToWidth(line, width, ""))
		c.traceWroteRow(screenRow)
	}
}

// repaintChromeRows paints screen rows [windowH+1, height] from the canvas
// chrome band [contentEnd, len(canvas)), keeping chrome pinned at the bottom.
func (c *Compositor) repaintChromeRows(buf *strings.Builder, canvas []string, windowH, height, contentEnd, width int) {
	for screenRow := windowH + 1; screenRow <= height; screenRow++ {
		i := contentEnd + (screenRow - windowH - 1)
		line := ""
		if i >= 0 && i < len(canvas) {
			line = canvas[i]
		}
		if c.unchangedRowChrome(canvas, i, screenRow, windowH) {
			continue
		}
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[2K", screenRow))
		buf.WriteString(truncateToWidth(line, width, ""))
		c.traceWroteRow(screenRow)
	}
}

// emitScrollbackAdvance advances the viewport from the previous top `from`
// (= the row currently at the top of the screen) to the new top `to`, pushing
// the scrolled-off rows into terminal scrollback exactly once.
//
// The screen currently shows rows [c.vt, c.vt+H) where H is the transcript
// window height; normally c.vt == from, but after a transient mid-stream
// shrink the window can be dipped BELOW the watermark (c.vt < scrollTop =
// from) — in that regime emitSteadyScroll bypasses the physical-identity
// mechanism entirely and re-emits top-down. After the advance the window
// must show [to, to+H). The rows that enter scrollback are [from, to) — the
// old top. The steady mechanism writes only the
// rows that were NOT already on screen, namely [from+H, to+H) (clamped to the
// transcript), at the region bottom, each followed by \n. Every \n scrolls the
// region: the top row (one of the old visible rows, then each freshly written
// row as it reaches the top) moves into scrollback. Writing from the first
// not-yet-visible row guarantees no already-visible row is ever rewritten, so
// nothing is duplicated. When chrome is pinned the scroll is confined to the
// transcript region via DECSTBM.
func (c *Compositor) emitScrollbackAdvance(buf *strings.Builder, canvas []string, from, to, width, height int) {
	if from >= to {
		return
	}
	windowH, scrollBot := c.transcriptWindow(buf, height)
	contentEnd := max(0, len(canvas)-c.chromeH)
	if c.prevLines == nil {
		c.emitFirstFrameScroll(buf, canvas, to, windowH, contentEnd, width)
	} else {
		c.emitSteadyScroll(buf, canvas, from, to, windowH, scrollBot, contentEnd, width)
	}
	if c.chromeH > 0 {
		c.resetScrollRegion(buf)
	}
}

// transcriptWindow returns the transcript window height and the scroll-region
// bottom row for this frame. When chrome is pinned it also confines the scroll
// to the transcript region by emitting DECSTBM into buf.
func (c *Compositor) transcriptWindow(buf *strings.Builder, height int) (windowH, scrollBot int) {
	windowH, scrollBot = height, height
	if c.chromeH > 0 {
		windowH = max(1, height-c.chromeH)
		scrollBot = windowH
		c.setScrollRegion(buf, scrollBot)
	}
	return windowH, scrollBot
}

// emitFirstFrameScroll writes the whole transcript top-down from the region's
// top row, advancing with \n. The screen fills top-to-bottom; once full, each
// further \n scrolls the region's top row into scrollback. Net effect: exactly
// [0, to) in scrollback and [to, to+windowH) on screen, with no out-of-order
// bottom writes, so nothing is duplicated.
func (c *Compositor) emitFirstFrameScroll(buf *strings.Builder, canvas []string, to, windowH, contentEnd, width int) {
	buf.WriteString("\x1b[1;1H")
	writeTo := min(to+windowH, contentEnd)
	for i := 0; i < writeTo; i++ {
		if i > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString("\r\x1b[2K")
		buf.WriteString(truncateToWidth(canvas[i], width, ""))
	}
}

// emitSteadyScroll advances the transcript window from `from` to `to`, pushing
// the scrolled-off rows [from, to) into scrollback exactly once and in order.
//
// The correct mechanism depends on whether the previous window was FULL:
//
//   - Full window (every region row held real content): the canvas layout is
//     stable frame-to-frame, so the newly revealed rows are exactly
//     [from+windowH, to+windowH). Writing those at the region bottom with a
//     line-feed each scrolls the previously-visible rows [from, to) off the
//     top into scrollback naturally. This is the cheap incremental path.
//
//   - Partial / re-anchored window (blank padding, or content that re-flowed
//     because the transcript grew into an empty region): canvas row indices do
//     NOT correspond across frames — content is top-anchored (header) or
//     bottom-anchored (first message) — so index-based "newly revealed" math
//     is unsound and either duplicates rows (header) or drops them (first
//     message). The sound fallback is a top-down re-emit of [from, to+windowH):
//     rows [from, to) scroll into scrollback in order, rows [to, to+windowH)
//     fill the screen. This is exactly once, gapless, for both anchorings.
//
//   - Dipped window (c.vt < scrollTop): a transient mid-stream shrink
//     anchored the window BELOW the scrollback watermark (windowTop's
//     partial-shrink regime), so the physical screen top is c.vt, NOT from
//     (= max(c.vt, scrollTop)). The full-window branch's line-feeds would
//     push the physical top rows [c.vt, c.vt+n) — already in scrollback —
//     in a second time (duplicates) while the watermark skips rows that
//     never physically scrolled (lost rows): the streaming-stutter bug.
//     The top-down path does not depend on physical row identity, so it is
//     the only sound emission while dipped.
func (c *Compositor) emitSteadyScroll(buf *strings.Builder, canvas []string, from, to, windowH, scrollBot, contentEnd, width int) {
	if !c.prevWindowFull(windowH) || c.vt < c.scrollTop {
		c.emitTopDownScroll(buf, canvas, from, to, windowH, contentEnd, width)
		return
	}
	writeFrom := from + windowH
	writeTo := min(to+windowH, contentEnd)
	writeFrom = max(writeFrom, c.scrollTop)
	writeFrom = max(writeFrom, 0)
	buf.WriteString(fmt.Sprintf("\x1b[%d;1H", scrollBot))
	for i := writeFrom; i < writeTo; i++ {
		buf.WriteString("\n")
		buf.WriteString("\r\x1b[2K")
		buf.WriteString(truncateToWidth(canvas[i], width, ""))
	}
}

// emitTopDownScroll re-emits the window top-down from canvas row `from`: it
// homes to the region top, then writes rows [from, writeTo) advancing with
// line-feeds. The first windowH rows fill the screen; each subsequent
// line-feed scrolls the region, pushing one of the leading rows into
// scrollback. Net effect: rows [from, to) land in scrollback in order (exactly
// once) and rows [to, to+windowH) remain on screen. Rows before `from` are
// already in scrollback (watermark) and are not rewritten.
func (c *Compositor) emitTopDownScroll(buf *strings.Builder, canvas []string, from, to, windowH, contentEnd, width int) {
	buf.WriteString("\x1b[1;1H")
	writeTo := min(to+windowH, contentEnd)
	if from < c.scrollTop {
		from = c.scrollTop
	}
	if from < 0 {
		from = 0
	}
	for i := from; i < writeTo; i++ {
		if i > from {
			buf.WriteString("\n")
		}
		buf.WriteString("\r\x1b[2K")
		buf.WriteString(truncateToWidth(canvas[i], width, ""))
	}
}

// prevWindowFull reports whether every transcript region row of the previous
// frame's window held real content (no blank padding). A partial window —
// content top- or bottom-anchored with blanks — has at least one blank region
// row; taking the "visible rows scroll off naturally" path would push those
// blanks into scrollback and lose real content, so it must re-emit everything.
func (c *Compositor) prevWindowFull(windowH int) bool {
	if c.prevLines == nil {
		return false
	}
	// prevLines is the PREVIOUS canvas: its chrome band is prevChromeH rows,
	// not chromeH. Using the current chromeH here miscomputes the previous
	// content end by the chrome delta and could mark a shifted window "full"
	// on stale rows (the watermark desync e6d8a2f worked around with a full
	// reset on every chrome change).
	contentEnd := len(c.prevLines) - c.prevChromeH
	if contentEnd < c.vt+windowH {
		return false
	}
	for r := c.vt; r < c.vt+windowH && r < contentEnd; r++ {
		if strings.TrimSpace(ansi.Strip(c.prevLines[r])) == "" {
			return false
		}
	}
	return true
}

// setScrollRegion emits DECSTBM to confine scrolling to [1, bot] (1-indexed)
// and records it. The cursor is homed by the terminal per the DEC spec.
func (c *Compositor) setScrollRegion(buf *strings.Builder, bot int) {
	if c.regionBot == bot {
		return
	}
	buf.WriteString(fmt.Sprintf("\x1b[1;%dr", bot))
	c.regionBot = bot
}

// resetScrollRegion restores full-screen scrolling (\x1b[r) and records it.
func (c *Compositor) resetScrollRegion(buf *strings.Builder) {
	if c.regionBot == 0 {
		return
	}
	buf.WriteString("\x1b[r")
	c.regionBot = 0
}

// unchangedRowTranscript reports whether transcript canvas row i (at viewport
// top vt) has the same bytes as the row the terminal currently shows at that
// screen position. The previous frame's row at the same screen position was
// prevLines[i - vt + c.vt].
