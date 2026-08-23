// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"context"
	"strings"
)

// ReplayScrollback re-emits the canvas rows [from, to) into the terminal
// scrollback so a detached view's committed history becomes physically
// present (and scrollable) before the view's live window repaints. It is the
// protocol primitive behind the multi-agent ReplayRunner (tui/agentctx) and
// runs OFF the render loop on the runner's goroutine.
//
// Ownership and serialization (plan T3):
//   - It owns ONLY scrollback emission. It never touches the live compositor
//     baseline: prevLines, scrollTop, and vt are left untouched. The final
//     watermark is RETURNED; the caller hands it to the command loop to be
//     applied there (single-owner rule R1).
//   - Each chunk is one terminal write under c.mu — the same lock that
//     serializes every live-frame Render — so replay bytes never interleave
//     with a live-frame commit. c.mu is RELEASED between chunks, so a huge
//     history never monopolizes the terminal for longer than one chunk.
//   - Emission is top-down with line-feed scrolls confined to the transcript
//     region (DECSTBM), the emitTopDownScroll algorithm: rows [from, to) are
//     pushed into scrollback in order, exactly once.
//   - ctx is honored BETWEEN chunks: cancellation stops the replay and
//     returns the rows emitted so far, so a new view switch supersedes
//     without waiting for the full history.
//   - On every return path (success, cancel, write error) the scroll region
//     is restored and the cursor homed, so the render loop resumes over a
//     clean terminal state. A write error is contained to this call.
//
// The returned int is the absolute canvas row the emission reached (== to on
// success; the next un-emitted row on cancel/error): the caller's "rows
// physically present in scrollback" bookkeeping advances by exactly that.
func (c *Compositor) ReplayScrollback(ctx context.Context, canvas []string, from, to, width, height, chunkRows int) (int, error) {
	from, to = clampReplayRange(from, to, len(canvas))
	if from >= to {
		return from, nil
	}
	if chunkRows < 1 {
		chunkRows = 64
	}
	if width < 20 {
		width = 80
	}
	if height < 10 {
		height = 24
	}

	var retErr error
	pos := from
	defer func() { c.replayFinish() }() // always restore region + home cursor
	for pos < to {
		if err := ctx.Err(); err != nil {
			return pos, err
		}
		end := min(pos+chunkRows, to)
		if err := c.replayChunk(canvas, pos, end, from, width, height); err != nil {
			retErr = err
			return pos, retErr
		}
		pos = end
	}
	return pos, nil
}

// clampReplayRange bounds [from, to) to the canvas and orders them.
func clampReplayRange(from, to, canvasLen int) (int, int) {
	if from < 0 {
		from = 0
	}
	if to > canvasLen {
		to = canvasLen
	}
	return from, to
}

// replayChunk atomically writes canvas rows [from, to) under c.mu. The very
// first chunk of a run (globalFrom == from) opens the transcript scroll
// region and homes to its top; each row is erased and rewritten, advancing
// with line-feeds — once the region fills, each \n pushes the region's top
// row into scrollback (the emitTopDownScroll exactly-once pattern). The
// cursor is left immediately after the last written row so the next chunk
// continues the stream seamlessly.
func (c *Compositor) replayChunk(canvas []string, from, to, globalFrom, width, height int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	windowH := height - c.chromeH
	if windowH < 1 {
		windowH = 1
	}

	var buf strings.Builder
	buf.WriteString("\x1b[?2026h")
	if from == globalFrom {
		if c.chromeH > 0 {
			c.setScrollRegion(&buf, windowH) // DECSTBM also homes the cursor
		} else {
			buf.WriteString("\x1b[1;1H")
		}
	}
	for i := from; i < to; i++ {
		if i > globalFrom {
			buf.WriteString("\n")
		}
		buf.WriteString("\r\x1b[2K")
		buf.WriteString(truncateToWidth(canvas[i], width, ""))
	}
	buf.WriteString("\x1b[?2026l")
	_, err := c.writeFrame(&buf)
	return err
}

// replayFinish restores the terminal after a replay run: full-screen scroll
// region and cursor homed to the region top. The resume frame repaints the
// window and repositions the cursor precisely; this just leaves a sane state.
func (c *Compositor) replayFinish() {
	c.mu.Lock()
	defer c.mu.Unlock()
	var buf strings.Builder
	buf.WriteString("\x1b[?2026h")
	c.resetScrollRegion(&buf)
	buf.WriteString("\x1b[1;1H")
	buf.WriteString("\x1b[?2026l")
	_, _ = c.terminal.Write([]byte(buf.String()))
}
