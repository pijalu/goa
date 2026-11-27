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
	"sync"
	"unicode"
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

	Reset         = CSI + "0m"
	Bold          = CSI + "1m"
	Faint         = CSI + "2m"
	Italic        = CSI + "3m"
	Underline     = CSI + "4m"
	Strikethrough = CSI + "9m"
	Reverse       = CSI + "7m"
	ReverseReset  = CSI + "27m"

	// Partial resets preserve the active background color while resetting
	// only the requested attribute. These are required when styling fragments
	// inside background-colored blocks (e.g. tool execution output) so the
	// outer background is not killed by an inner Reset.
	FgReset             = CSI + "39m" // default foreground color
	BgReset             = CSI + "49m" // default background color
	UnderlineColorReset = CSI + "59m" // default underline color (ISO 8613-6)
	BoldReset           = CSI + "22m" // normal intensity (resets bold and faint)
	FaintReset          = CSI + "22m" // same as BoldReset
	ItalicReset         = CSI + "23m"
	UnderlineReset      = CSI + "24m"
	StrikethroughReset  = CSI + "29m"
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
func FgRGB(r, g, b uint8) string {
	return rgbSequence("38", r, g, b)
}

// BgRGB returns a true-color background ANSI sequence.
func BgRGB(r, g, b uint8) string {
	return rgbSequence("48", r, g, b)
}

// rgbSequence builds CSI <layer>;2;r;g;b m without fmt: color sequences are
// emitted for every styled span of every rendered row, so this sits on the hot
// path of the streaming renderer.
func rgbSequence(layer string, r, g, b uint8) string {
	var buf [24]byte
	n := copy(buf[:], CSI)
	n += copy(buf[n:], layer)
	n += copy(buf[n:], ";2;")
	n = appendDecimal(buf[:], n, r)
	buf[n] = ';'
	n = appendDecimal(buf[:], n+1, g)
	buf[n] = ';'
	n = appendDecimal(buf[:], n+1, b)
	buf[n] = 'm'
	return string(buf[:n+1])
}

// appendDecimal writes v's decimal digits at buf[n:] and returns the next
// free offset.
func appendDecimal(buf []byte, n int, v uint8) int {
	switch {
	case v >= 100:
		buf[n] = '0' + v/100
		buf[n+1] = '0' + (v/10)%10
		buf[n+2] = '0' + v%10
		return n + 3
	case v >= 10:
		buf[n] = '0' + v/10
		buf[n+1] = '0' + v%10
		return n + 2
	default:
		buf[n] = '0' + v
		return n + 1
	}
}

// HexToRGB converts a hex color string (#RGB or #RRGGBB) to RGB components.
// Malformed input yields the neutral grey (128,128,128); a partially valid
// string keeps the components parsed before the invalid one, matching the
// previous fmt.Sscanf-based parser.
func HexToRGB(hex string) (r, g, b uint8) {
	hex = strings.TrimPrefix(hex, "#")
	switch len(hex) {
	case 3:
		// #RGB expands to #RRGGBB.
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	case 6:
	default:
		return 128, 128, 128
	}
	var vals [3]uint8
	for i := 0; i < 3; i++ {
		hi, ok := hexDigit(hex[2*i])
		if !ok {
			return vals[0], vals[1], vals[2]
		}
		lo, ok := hexDigit(hex[2*i+1])
		if !ok {
			return vals[0], vals[1], vals[2]
		}
		vals[i] = hi<<4 | lo
	}
	return vals[0], vals[1], vals[2]
}

// hexDigit decodes one hexadecimal digit. Unlike strconv it reports failure
// instead of returning an error value, keeping the colour path allocation-free.
func hexDigit(c byte) (uint8, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// hexSequences memoizes the escape sequence of every hex colour seen, keyed
// per layer (fore/background) with no key allocation. Themes use a small fixed
// palette, but inline markdown styling asks for the same few colours thousands
// of times per frame, so the lookup removes both the decode and the sequence
// build from the render hot path.
var (
	fgSequences sync.Map // map[string]string keyed by hex
	bgSequences sync.Map // map[string]string keyed by hex
)

// cachedSequence returns the memoized sequence for hex from cache, building it
// with build on first use.
func cachedSequence(cache *sync.Map, hex string, build func(string) string) string {
	if v, ok := cache.Load(hex); ok {
		return v.(string)
	}
	seq := build(hex)
	cache.Store(hex, seq)
	return seq
}

// Fg returns the ANSI foreground color sequence for a hex color. Results are
// memoized: rendered rows ask for the same theme colours on every frame.
func Fg(hex string) string {
	return cachedSequence(&fgSequences, hex, func(h string) string {
		r, g, b := HexToRGB(h)
		return FgRGB(r, g, b)
	})
}

// Hyperlink wraps text in an OSC-8 hyperlink sequence so supporting
// terminals render it as a clickable link to url. Format:
// ESC ] 8 ; params ; url BEL text ESC ] 8 ; ; BEL. An empty url closes the
// link (matching AnsiState.processHyperlink). Text with an empty url is
// returned unchanged.
func Hyperlink(url, text string) string {
	if url == "" {
		return text
	}
	return Esc + "]8;;" + url + "\x07" + text + Esc + "]8;;\x07"
}

// Bg returns the ANSI background color sequence for a hex color. Memoized for
// the same reason as Fg.
func Bg(hex string) string {
	return cachedSequence(&bgSequences, hex, func(h string) string {
		r, g, b := HexToRGB(h)
		return BgRGB(r, g, b)
	})
}

// UnderlineColorRGB returns an ISO 8613-6 true-color underline-color sequence
// (SGR 58). Like ratatui's cell underline_color, it only sets the color used
// to draw underlines; it does NOT enable underlining — pair it with SGR 4
// (ansi.Underline) for that.
//
// Legacy Windows conhost does not implement SGR 58: the sequence is silently
// ignored and underlines degrade to the terminal default underline color,
// while plain SGR 4 underlining remains fully available (see
// docs/research/ratatui-tui-enhancements.md §4.3).
func UnderlineColorRGB(r, g, b uint8) string {
	return fmt.Sprintf(CSI+"58;2;%d;%d;%dm", r, g, b)
}

// UnderlineColor returns the ISO 8613-6 underline-color (SGR 58) sequence for
// a hex color. See UnderlineColorRGB for semantics and conhost degradation.
func UnderlineColor(hex string) string {
	r, g, b := HexToRGB(hex)
	return UnderlineColorRGB(r, g, b)
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
	if strings.IndexByte(s, 0x1b) < 0 {
		return s // fast path: no escape sequences to strip
	}
	return ansiRe.ReplaceAllString(s, "")
}

// Sanitize makes untrusted text (file contents, command output) safe to show
// in a terminal: ESC becomes the two printable characters `\e`, other control
// runes become '?', and invalid UTF-8 is replaced. Tabs and newlines are kept
// (tabs expand at render time; newlines stay meaningful for line splitting).
//
// Unlike Strip, which drops escape sequences produced by goa itself, Sanitize
// keeps foreign sequences *visible* — a searched file containing "\e[2K" must
// display as literal text, never erase the user's screen.
func Sanitize(s string) string {
	if strings.IndexByte(s, 0x1b) < 0 && utf8.ValidString(s) && strings.IndexFunc(s, unicode.IsControl) < 0 {
		return s // fast path: nothing to do
	}
	s = strings.ToValidUTF8(s, "�")
	var out strings.Builder
	out.Grow(len(s))
	for _, r := range s {
		switch {
		case r == 0x1b:
			out.WriteString(`\e`)
		case r == '\t' || r == '\n' || r == '\r':
			out.WriteRune(r)
		case unicode.IsControl(r):
			out.WriteByte('?')
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

// Width returns the display width of a string, ignoring ANSI codes.
// It is grapheme-cluster-aware: a cluster's width is the width of its base
// rune (combining marks, ZWJ joiners, variation selectors, and regional
// indicator pairs add no extra width). This matches what the terminal
// actually renders — essential for correct cursor placement with emoji
// (e.g. the ZWJ family emoji 👨‍👩‍👧 renders as 2 columns, not the 6 that a
// naive per-rune sum would yield). Tabs expand to terminal tab stops.
func Width(s string) int {
	// Fast path: printable ASCII plus complete CSI/OSC escapes (the overwhelmingly
	// common case for rendered terminal rows). Every glyph there is a single-rune
	// grapheme cluster of width 1, so the printable byte count IS the display
	// width — no Strip (regexp) and no grapheme segmentation needed. Tabs, other
	// control bytes, non-ASCII runes and unknown escape forms fall through to the
	// authoritative cluster path below, which returns the identical result.
	if w, ok := asciiWidth(s); ok {
		return w
	}
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

// asciiWidth reports the display width of s (and true) when s consists solely
// of printable ASCII runes and complete CSI/OSC sequences. Every other input —
// tabs, control bytes, DEL, non-ASCII runes (wide, combining, ZWJ, flags) or an
// incomplete/unknown escape — returns (0, false) so the caller uses the
// grapheme-cluster path instead.
func asciiWidth(s string) (int, bool) {
	w := 0
	for i := 0; i < len(s); {
		b := s[i]
		if b == 0x1b {
			end := scanAnsiLikeRegex(s, i)
			if end == 0 {
				return 0, false
			}
			i = end
			continue
		}
		if b < 0x20 || b >= 0x7f {
			return 0, false
		}
		w++
		i++
	}
	return w, true
}

// scanAnsiLikeRegex returns the exclusive end of the escape sequence at start
// when ansiRe — the authority used by Strip — would match there, else 0. The
// fast path must skip EXACTLY what Strip removes: scanAnsiSequence is the
// terminal's parser (it tolerates stray bytes inside a CSI), while Strip uses
// the stricter regex grammar. Diverging here would let Width and Strip disagree
// on malformed input.
func scanAnsiLikeRegex(s string, start int) int {
	if start+1 >= len(s) || s[start] != 0x1b {
		return 0
	}
	switch s[start+1] {
	case ']':
		return scanOSCLikeRegex(s, start+2)
	case '[':
		return scanCSILikeRegex(s, start+2)
	}
	return 0
}

// scanOSCLikeRegex scans OSC payload from i: ESC ] [^\x07\x1b]* ( BEL | ESC \ ).
// Returns the exclusive end, or 0 when the sequence is not terminated.
func scanOSCLikeRegex(s string, i int) int {
	for ; i < len(s); i++ {
		switch s[i] {
		case 0x07:
			return i + 1
		case 0x1b:
			if i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
			return 0
		}
	}
	return 0
}

// scanCSILikeRegex scans a CSI from i: [0-9;?]* [ -/]* [@-~]. Returns the
// exclusive end, or 0 when the sequence never reaches a final byte.
func scanCSILikeRegex(s string, i int) int {
	for i < len(s) && isCSIParam(s[i]) {
		i++
	}
	for i < len(s) && s[i] >= ' ' && s[i] <= '/' {
		i++
	}
	if i < len(s) && s[i] >= '@' && s[i] <= '~' {
		return i + 1
	}
	return 0
}

// isCSIParam reports whether b is a CSI parameter byte (digits, ';' and '?').
func isCSIParam(b byte) bool {
	return b >= '0' && b <= '9' || b == ';' || b == '?'
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
		if s[i] == 0x1b {
			if end := scanAnsiSequence(s, i); end > 0 {
				flushLit()
				segs = append(segs, ansiSegment{text: s[i:end], esc: true})
				i = end
				continue
			}
		}
		lit.WriteByte(s[i])
		i++
	}
	flushLit()
	return segs
}

// scanAnsiSequence returns the exclusive end of the complete CSI or OSC
// sequence at start, or zero when start does not begin a complete sequence.
// OSC targets are protocol bytes even when they contain printable URLs.
func scanAnsiSequence(s string, start int) int {
	if start+1 >= len(s) {
		return 0
	}
	switch s[start+1] {
	case '[':
		// CSI: ESC [ ... final byte 0x40-0x7E.
		for end := start + 2; end < len(s); end++ {
			if s[end] >= 0x40 && s[end] <= 0x7E {
				return end + 1
			}
		}
	case ']':
		// OSC: ESC ] ... BEL or ST.
		return scanOSC(s, start+2)
	}
	return 0
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
// Trailing spaces are never dropped: they are wrapped onto the last line(s)
// so that editor cursor placement and rendered text stay in agreement.
func Wrap(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}

	// Fast path: text fits on one line, return as-is preserving all spaces.
	if !strings.ContainsRune(text, '\n') && Width(text) <= width {
		return []string{text}
	}

	// Split trailing spaces from the body so they are not dropped by splitWords.
	body, trailing := splitBodyTrailing(text)

	if body == "" {
		if trailing == "" {
			return []string{""}
		}
		return wrapSpaces(trailing, width)
	}

	words := splitWords(body)
	if len(words) == 0 {
		return []string{body}
	}

	w := newLineWrapper(width)
	for i, word := range words {
		w.addWord(word, i == 0)
	}
	lines := w.result()

	if trailing != "" {
		lines = appendTrailingSpaces(lines, trailing, width, w.state)
	}
	return lines
}

// splitBodyTrailing splits text into its non-trailing-space body and any
// trailing spaces.
func splitBodyTrailing(text string) (body, trailing string) {
	runes := []rune(text)
	bodyEnd := len(runes)
	for bodyEnd > 0 && runes[bodyEnd-1] == ' ' {
		bodyEnd--
	}
	return string(runes[:bodyEnd]), string(runes[bodyEnd:])
}

// lineWrapper accumulates wrapped lines for a single paragraph. It tracks the
// active ANSI state so continuation lines can re-emit open attributes.
type lineWrapper struct {
	width     int
	lines     []string
	line      strings.Builder
	lineWidth int
	state     *AnsiState
}

func newLineWrapper(width int) *lineWrapper {
	return &lineWrapper{width: width, state: &AnsiState{}}
}

func (w *lineWrapper) addWord(word string, isFirst bool) {
	preState := w.state.GetActiveCodes()
	updateStateFromSGR(w.state, word)
	ww := Width(word)

	if ww > w.width {
		w.addWideWord(word, preState)
		return
	}
	w.addFittingWord(word, ww, isFirst, preState)
}

func (w *lineWrapper) addWideWord(word, preState string) {
	if w.lineWidth > 0 {
		w.flushLine()
	}
	broken := breakLongWord(word, w.width)
	chunkState := &AnsiState{}
	updateStateFromSGR(chunkState, preState)
	for j, bw := range broken {
		activeBefore := chunkState.GetActiveCodes()
		updateStateFromSGR(chunkState, bw)
		if j < len(broken)-1 {
			// Close bleeding attributes on this physical row, then restore the
			// logical state on the next. This keeps hyperlinks off panel borders
			// and padding without dropping the link from continuation text.
			w.lines = append(w.lines, activeBefore+bw+Reset+chunkState.GetLineEndReset())
			continue
		}
		w.line.WriteString(activeBefore + bw)
		w.lineWidth = Width(bw)
	}
}

func (w *lineWrapper) addFittingWord(word string, ww int, isFirst bool, preState string) {
	space := 0
	if !isFirst {
		space = 1
	}
	if w.lineWidth+space+ww > w.width && w.lineWidth > 0 {
		// Flush with line-end reset for underline/hyperlink bleed.
		w.line.WriteString(w.state.GetLineEndReset())
		w.lines = append(w.lines, w.line.String())
		w.line.Reset()
		w.line.WriteString(preState) // Use PRE-word state for continuation header.
		w.lineWidth = 0
		space = 0
	}
	if space > 0 {
		w.line.WriteString(" ")
		w.lineWidth++
	}
	w.line.WriteString(word)
	w.lineWidth += ww
}

func (w *lineWrapper) flushLine() {
	if w.lineWidth > 0 {
		w.lines = append(w.lines, w.line.String())
		w.line.Reset()
		w.lineWidth = 0
	}
}

func (w *lineWrapper) result() []string {
	if w.line.Len() > 0 {
		w.lines = append(w.lines, w.line.String())
	}
	return w.lines
}

// wrapSpaces wraps a string composed only of spaces to the given width.
func wrapSpaces(text string, width int) []string {
	var lines []string
	var line strings.Builder
	lineWidth := 0
	for _, ch := range text {
		if ch != ' ' {
			continue
		}
		if lineWidth+1 > width {
			if line.Len() > 0 {
				lines = append(lines, line.String())
			}
			line.Reset()
			lineWidth = 0
		}
		line.WriteRune(ch)
		lineWidth++
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// appendTrailingSpaces wraps trailing spaces onto the last line(s), preserving
// the active ANSI state so continuation lines carry open attributes.
func appendTrailingSpaces(lines []string, trailing string, width int, state *AnsiState) []string {
	if len(lines) == 0 {
		lines = append(lines, "")
	}
	lastIdx := len(lines) - 1
	var cur strings.Builder
	cur.WriteString(lines[lastIdx])
	curWidth := Width(lines[lastIdx])
	preState := state.GetActiveCodes()
	for _, ch := range trailing {
		if ch != ' ' {
			continue
		}
		if curWidth+1 > width {
			// Flush the current line and start a new one with the active state.
			cur.WriteString(state.GetLineEndReset())
			lines[lastIdx] = cur.String()
			cur.Reset()
			cur.WriteString(preState)
			curWidth = 0
			lines = append(lines, "")
			lastIdx = len(lines) - 1
		}
		cur.WriteRune(ch)
		curWidth++
	}
	if cur.Len() > 0 {
		lines[lastIdx] = cur.String()
	}
	return lines
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
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}
	for _, seg := range splitAnsiSegments(text) {
		if seg.esc {
			cur.WriteString(seg.text)
			continue
		}
		for _, ch := range seg.text {
			if ch == ' ' {
				flush()
				continue
			}
			cur.WriteRune(ch)
		}
	}
	flush()
	return words
}

// breakLongWord breaks a word (which may contain ANSI) into chunks that fit
// within the given visual width.
func breakLongWord(word string, width int) []string {
	var chunks []string
	var chunk strings.Builder
	cw := 0
	for _, seg := range splitAnsiSegments(word) {
		if seg.esc {
			chunk.WriteString(seg.text)
			continue
		}
		gr := uniseg.NewGraphemes(seg.text)
		for gr.Next() {
			cluster := gr.Str()
			clusterWidth := clusterBaseWidth(cluster)
			if cw > 0 && cw+clusterWidth > width {
				chunks = append(chunks, chunk.String())
				chunk.Reset()
				cw = 0
			}
			chunk.WriteString(cluster)
			cw += clusterWidth
		}
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
