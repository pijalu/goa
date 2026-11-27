// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

// lineTransformCache memoizes the result of a per-line transform over a GROWING
// line list, so that re-transforming a long transcript each frame costs only the
// lines that actually changed.
//
// It exists for the streaming render path (see assistantMessage.renderFrame):
// every chunk used to re-pad EVERY line of the whole answer, and padding
// measures each line's display width — O(answer) width scans per chunk, O(n²)
// over a turn. The streamed markdown renderer already knows that a leading
// prefix of its output is stable (closed markdown blocks); this cache turns that
// knowledge into skipped work.
//
// Single responsibility: it knows nothing about markdown, padding or widths. It
// only remembers a transformed prefix, keyed by width, for a caller that can
// guarantee the leading lines it hands in are unchanged.
type lineTransformCache struct {
	// width is the width the cached lines were transformed for; a different
	// width invalidates the whole cache.
	width int
	// src is the number of leading source lines covered by out.
	src int
	// out holds the transformed form of the first src lines.
	out []string
}

// apply returns fn applied to every line of lines. When the leading `stable`
// lines did not change since the previous call (and the width is unchanged and
// fn is deterministic for that width), their cached results are reused and only
// the remaining lines are transformed. The returned slice is freshly allocated
// and owned by the caller; the cache never aliases it.
func (c *lineTransformCache) apply(lines []string, width, stable int, fn func(string) string) []string {
	// A text that shrank, an edit inside the formerly-stable region, or a
	// width change makes the memoized prefix meaningless: start over.
	if c.width != width || c.src > stable || c.src > len(lines) {
		c.out = c.out[:0]
		c.src = 0
		c.width = width
	}
	// Extend the cache over lines that just became stable — they will never be
	// transformed again.
	for c.src < stable && c.src < len(lines) {
		c.out = append(c.out, fn(lines[c.src]))
		c.src++
	}
	out := make([]string, 0, len(lines))
	out = append(out, c.out...)
	for i := c.src; i < len(lines); i++ {
		out = append(out, fn(lines[i]))
	}
	return out
}
