// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package ansi provides low-level ANSI escape sequence helpers, true-color
// conversion, visual width calculation, and ANSI-aware text wrapping.
package ansi

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// TabWidth is the terminal tab stop interval used for width calculations.
const TabWidth = 8

// Escape sequences.
const (
	Esc = "\x1b"
	CSI = Esc + "["

	HideCursor = CSI + "?25l"
	ShowCursor = CSI + "?25h"

	ClearLine           = CSI + "2K"
	ClearFromCursorDown = CSI + "0J"
	ClearScreen         = CSI + "2J"

	SaveCursor    = Esc + "7"
	RestoreCursor = Esc + "8"

	Reset        = CSI + "0m"
	Bold         = CSI + "1m"
	Faint        = CSI + "2m"
	Italic       = CSI + "3m"
	Underline    = CSI + "4m"
	Reverse      = CSI + "7m"
	ReverseReset = CSI + "27m"

	// Partial resets preserve the active background color while resetting
	// only the requested attribute. These are required when styling fragments
	// inside background-colored blocks (e.g. tool execution output) so the
	// outer background is not killed by an inner Reset.
	FgReset     = CSI + "39m" // default foreground color
	BgReset     = CSI + "49m" // default background color
	BoldReset   = CSI + "22m" // normal intensity (resets bold and faint)
	FaintReset  = CSI + "22m" // same as BoldReset
	ItalicReset = CSI + "23m"
)

// MoveUp returns the escape sequence to move the cursor up n lines.
func MoveUp(n int) string { return fmt.Sprintf(CSI+"%dA", n) }

// MoveDown returns the escape sequence to move the cursor down n lines.
func MoveDown(n int) string { return fmt.Sprintf(CSI+"%dB", n) }

// MoveRight returns the escape sequence to move the cursor right n columns.
func MoveRight(n int) string { return fmt.Sprintf(CSI+"%dC", n) }

// MoveLeft returns the escape sequence to move the cursor left n columns.
func MoveLeft(n int) string { return fmt.Sprintf(CSI+"%dD", n) }

// MoveToCol returns the escape sequence to move the cursor to column n (1-indexed).
func MoveToCol(n int) string { return fmt.Sprintf(CSI+"%dG", n) }

// FgRGB returns a true-color foreground ANSI sequence.
func FgRGB(r, g, b uint8) string { return fmt.Sprintf(CSI+"38;2;%d;%d;%dm", r, g, b) }

// BgRGB returns a true-color background ANSI sequence.
func BgRGB(r, g, b uint8) string { return fmt.Sprintf(CSI+"48;2;%d;%d;%dm", r, g, b) }

// HexToRGB converts a hex color string (#RGB or #RRGGBB) to RGB components.
func HexToRGB(hex string) (r, g, b uint8) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return 128, 128, 128
	}
	var rv, gv, bv uint8
	fmt.Sscanf(hex, "%02x%02x%02x", &rv, &gv, &bv)
	return rv, gv, bv
}

// Fg returns the ANSI foreground color sequence for a hex color.
func Fg(hex string) string {
	r, g, b := HexToRGB(hex)
	return FgRGB(r, g, b)
}

// Bg returns the ANSI background color sequence for a hex color.
func Bg(hex string) string {
	r, g, b := HexToRGB(hex)
	return BgRGB(r, g, b)
}

// RenderWithCursor returns text with the character at cursorRunePos displayed
// in reverse video. If cursorRunePos is at the end of text, a space is shown
// in reverse video. Returns plain text if cursorRunePos is out of range.
func RenderWithCursor(text string, cursorRunePos int) string {
	if cursorRunePos < 0 {
		return text
	}
	byteOff := 0
	runeIdx := 0
	for _, r := range text {
		if runeIdx == cursorRunePos {
			runeBytes := string(r)
			before := text[:byteOff]
			after := text[byteOff+len(runeBytes):]
			return before + Reverse + runeBytes + Reset + after
		}
		byteOff += len(string(r))
		runeIdx++
	}
	return text + Reverse + " " + Reset
}

// ansiRe matches CSI and OSC escape sequences:
//   - OSC: ESC ] ... (BEL | ST)   e.g. window-title sets ESC]0;title␇
//   - CSI: ESC [ params intermediates final   e.g. color ESC[31m, clear ESC[2J
//
// This is a strict superset of the previous SGR-only regex (ESC[...m), so it
// also strips cursor moves, clears, and title sequences that have zero width.
var ansiRe = regexp.MustCompile(`\x1b(?:\][^\x07\x1b]*(?:\x07|\x1b\\)|\[[0-9;?]*[ -/]*[@-~])`)

// Strip removes ANSI escape sequences from a string.
func Strip(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// Width returns the display width of a string, ignoring ANSI codes.
// It is grapheme-cluster-aware: a cluster's width is the width of its base
// rune (combining marks, ZWJ joiners, variation selectors, and regional
// indicator pairs add no extra width). This matches what the terminal
// actually renders — essential for correct cursor placement with emoji
// (e.g. the ZWJ family emoji 👨‍👩‍👧 renders as 2 columns, not the 6 that a
// naive per-rune sum would yield). Tabs expand to terminal tab stops.
func Width(s string) int {
	plain := Strip(s)
	w := 0
	gr := uniseg.NewGraphemes(plain)
	for gr.Next() {
		cluster := gr.Str()
		if strings.ContainsRune(cluster, '\t') {
			w += TabWidth - (w % TabWidth)
			continue
		}
		w += clusterBaseWidth(cluster)
	}
	return w
}

// clusterBaseWidth returns the terminal display width of a single grapheme
// cluster: the width of its first (base) rune, since extenders/combining
// marks contribute zero columns. Returns 0 for an empty cluster.
func clusterBaseWidth(cluster string) int {
	return ClusterWidth(cluster)
}

// ClusterWidth returns the terminal display width of a single grapheme
// cluster: the width of its base rune. Exported so other packages (e.g. tui)
// can compute cluster-aware widths consistently with ansi.Width.
//
// Regional-indicator pairs (flags) are forced to 2: go-runewidth reports them
// as 1 in this version, but terminals render a flag glyph across 2 columns.
func ClusterWidth(cluster string) int {
	r, _ := utf8.DecodeRuneInString(cluster)
	if r == utf8.RuneError {
		return 0
	}
	if r >= 0x1F1E6 && r <= 0x1F1FF {
		return 2
	}
	return runewidth.RuneWidth(r)
}

// ExpandTabs replaces tab characters with spaces so that each tab advances
// the visual column to the next multiple of tabWidth. ANSI escape sequences
// are preserved and do not consume columns.
func ExpandTabs(s string, tabWidth int) string {
	if tabWidth <= 0 {
		tabWidth = TabWidth
	}
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var out strings.Builder
	col := 0
	esc := &escapeTracker{}
	for _, ch := range s {
		if esc.update(ch) {
			out.WriteRune(ch)
			continue
		}
		if ch == '\t' {
			spaces := tabWidth - (col % tabWidth)
			out.WriteString(strings.Repeat(" ", spaces))
			col += spaces
			continue
		}
		out.WriteRune(ch)
		col += runewidth.RuneWidth(ch)
	}
	return out.String()
}

// Truncate truncates a string to the given visual width, preserving ANSI
// codes in the kept portion. It is grapheme-cluster-aware so it never splits
// a multi-rune cluster (e.g. a ZWJ emoji) mid-sequence, which would leave a
// dangling joiner and corrupt rendering.
func Truncate(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	var out strings.Builder
	vw := 0
	for _, seg := range splitAnsiSegments(s) {
		if seg.esc {
			// ANSI escape sequences are zero-width and always preserved.
			out.WriteString(seg.text)
			continue
		}
		gr := uniseg.NewGraphemes(seg.text)
		for gr.Next() {
			cluster := gr.Str()
			cw := clusterBaseWidth(cluster)
			if vw+cw > maxWidth {
				return out.String()
			}
			vw += cw
			out.WriteString(cluster)
		}
	}
	return out.String()
}

// ansiSegment is a substring classified as either an ANSI escape sequence or
// literal text.
type ansiSegment struct {
	text string
	esc  bool
}

// splitAnsiSegments splits s into alternating ANSI-escape and literal-text
// segments. Used by grapheme-aware helpers (Truncate) to pass escape
// sequences through untouched while segmenting literal text by grapheme
// cluster.
func splitAnsiSegments(s string) []ansiSegment {
	var segs []ansiSegment
	var lit strings.Builder
	flushLit := func() {
		if lit.Len() > 0 {
			segs = append(segs, ansiSegment{text: lit.String()})
			lit.Reset()
		}
	}
	i := 0
	for i < len(s) {
		c := s[i]
		if c == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			// CSI: ESC [ ... final byte 0x40-0x7E
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7E) {
				j++
			}
			if j < len(s) {
				j++ // include final byte
				flushLit()
				segs = append(segs, ansiSegment{text: s[i:j], esc: true})
				i = j
				continue
			}
		}
		lit.WriteByte(c)
		i++
	}
	flushLit()
	return segs
}

// escapeTracker tracks whether we're inside an ANSI escape sequence.
type escapeTracker struct {
	active bool
}

// update processes a rune and returns true if it belongs to an escape sequence.
func (e *escapeTracker) update(ch rune) bool {
	if ch == '\x1b' {
		e.active = true
		return true
	}
	if e.active {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') {
			e.active = false
		}
		return true
	}
	return false
}

// charWidth removed: grapheme-aware width is handled by ClusterWidth and
// runewidth.RuneWidth at call sites. Tab expansion uses TabWidth directly.

// Wrap wraps a single paragraph (no newlines) to the given visual width,
// preserving ANSI escape sequences. It carries active ANSI attributes across
// wrapped lines by tracking open SGR codes and re-emitting them on
// continuation lines.
//
// IMPORTANT: When text fits within width without wrapping, spaces are preserved
// as-is. When wrapping is required, spaces are preserved as individual tokens
// to avoid collapsing multiple spaces or dropping leading/trailing whitespace.
func Wrap(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}

	// Fast path: text fits on one line, return as-is preserving all spaces.
	if !strings.ContainsRune(text, '\n') && Width(text) <= width {
		return []string{text}
	}

	words := splitWords(text)
	if len(words) == 0 {
		return []string{""}
	}

	var lines []string
	var line strings.Builder
	lineWidth := 0
	state := &AnsiState{}

	for i, w := range words {
		// Capture state BEFORE processing this word's ANSI sequences
		// (the word may contain reset codes that shouldn't affect the
		// current line's header state)
		preState := state.GetActiveCodes()

		// Update state from any ANSI sequences embedded in this word
		updateStateFromSGR(state, w)

		ww := Width(w)
		space := 0
		if i > 0 {
			space = 1
		}

		if ww > width {
			lines, lineWidth = flushAndBreakLong2(lines, &line, lineWidth, preState, w, width)
			continue
		}

		if lineWidth+space+ww > width && lineWidth > 0 {
			// Flush with line-end reset for underline/hyperlink bleed
			line.WriteString(state.GetLineEndReset())
			lines = append(lines, line.String())
			line.Reset()
			line.WriteString(preState) // Use PRE-word state for continuation header
			lineWidth = 0
			space = 0
		}

		if space > 0 {
			line.WriteString(" ")
			lineWidth++
		}
		line.WriteString(w)
		lineWidth += ww
	}

	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	return lines
}

// flushAndBreakLong2 handles a word that exceeds the line width, using AnsiState.
func flushAndBreakLong2(lines []string, line *strings.Builder, lineWidth int, preState, word string, width int) ([]string, int) {
	if lineWidth > 0 {
		lines = append(lines, line.String())
		line.Reset()
		lineWidth = 0
	}
	broken := breakLongWord(word, width)
	for j, bw := range broken {
		if j < len(broken)-1 {
			lines = append(lines, preState+bw+Reset)
		} else {
			line.WriteString(preState + bw)
			lineWidth = Width(bw)
		}
	}
	return lines, lineWidth
}

// updateStateFromSGR scans s for ANSI SGR and OSC 8 hyperlink sequences
// and updates state accordingly.
func updateStateFromSGR(state *AnsiState, s string) {
	i := 0
	for i < len(s) {
		if s[i] != '\x1b' {
			i++
			continue
		}
		if consumed := scanAndUpdateState(state, s, i); consumed > 0 {
			i += consumed
		} else {
			i++
		}
	}
}

// scanAndUpdateState scans a single escape sequence starting at position i in s.
// Returns the number of bytes consumed, or 0 if no complete sequence found.
func scanAndUpdateState(state *AnsiState, s string, i int) int {
	if i+1 >= len(s) {
		return 0
	}
	switch s[i+1] {
	case '[':
		end := scanCSI(s, i+2)
		if end > 0 {
			state.Process(s[i:end])
			return end - i
		}
	case ']':
		end := scanOSC(s, i+2)
		if end > 0 {
			state.Process(s[i:end])
			return end - i
		}
	}
	return 0
}

// scanCSI scans a CSI sequence starting at 'start' (position after ESC[).
// Returns the absolute end position (exclusive) or 0 if incomplete.
func scanCSI(s string, start int) int {
	for j := start; j < len(s); j++ {
		c := s[j]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return j + 1
		}
	}
	return 0
}

// scanOSC scans an OSC sequence starting at 'start' (position after ESC]).
// Ends at BEL (0x07) or ST (ESC \).
func scanOSC(s string, start int) int {
	for j := start; j < len(s); j++ {
		if s[j] == 0x07 {
			return j + 1
		}
		if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
			return j + 2
		}
	}
	return 0
}

// splitWords splits text on spaces while keeping ANSI sequences attached
// to the words they precede or follow.
func splitWords(text string) []string {
	var words []string
	var cur strings.Builder
	esc := &escapeTracker{}

	for _, ch := range text {
		if esc.update(ch) {
			cur.WriteRune(ch)
			continue
		}
		if ch == ' ' {
			if cur.Len() > 0 {
				words = append(words, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteRune(ch)
	}
	if cur.Len() > 0 {
		words = append(words, cur.String())
	}
	return words
}

// breakLongWord breaks a word (which may contain ANSI) into chunks that fit
// within the given visual width.
func breakLongWord(word string, width int) []string {
	var chunks []string
	var chunk strings.Builder
	cw := 0
	esc := &escapeTracker{}

	for _, ch := range word {
		if esc.update(ch) {
			chunk.WriteRune(ch)
			continue
		}
		if cw+1 > width {
			chunks = append(chunks, chunk.String())
			chunk.Reset()
			cw = 0
		}
		chunk.WriteRune(ch)
		cw++
	}
	if chunk.Len() > 0 {
		chunks = append(chunks, chunk.String())
	}
	return chunks
}

// extractTrailingSGR returns the active SGR sequence(s) at the end of a string.
func extractTrailingSGR(s string) string {
	lastReset := strings.LastIndex(s, Reset)
	if lastReset < 0 {
		return extractLastSGR(s)
	}
	after := s[lastReset+len(Reset):]
	return extractAllSGR(after)
}

func extractLastSGR(s string) string {
	idx := strings.LastIndex(s, CSI)
	if idx < 0 {
		return ""
	}
	end := idx + len(CSI)
	for end < len(s) {
		c := s[end]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			end++
			break
		}
		end++
	}
	seq := s[idx:end]
	if strings.HasSuffix(seq, "m") {
		return seq
	}
	return ""
}

func extractAllSGR(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		if i+1 < len(s) && s[i] == '\x1b' && s[i+1] == '[' {
			seq, next := scanSGRCode(s, i)
			if next > i {
				out.WriteString(seq)
				i = next
				continue
			}
		}
		i++
	}
	return out.String()
}

func scanSGRCode(s string, start int) (string, int) {
	i := start + 2
	for i < len(s) {
		if (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z') {
			i++
			seq := s[start:i]
			if strings.HasSuffix(seq, "m") {
				return seq, i
			}
			return "", i
		}
		i++
	}
	return "", start
}

// CountNewlines returns the number of \n characters in s.
func CountNewlines(s string) int {
	count := 0
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			break
		}
		count++
		s = s[i+1:]
	}
	return count
}
