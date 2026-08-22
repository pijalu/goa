// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"regexp"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/tools"
)

// Markdown source palette — reuses the diff review pager's GitHub-ish colors
// so both pagers read as one feature.
const (
	mdHeadingColor = "#58a6ff" // ATX headings
	mdFenceColor   = "#8b949e" // fence markers (``` / ~~~)
	mdQuoteColor   = "#3fb950" // blockquote markers
	mdBulletColor  = "#d29922" // list bullets and ordered numbers
	mdCodeColor    = "#a5d6ff" // inline code spans
)

// mdSpanRe matches, leftmost-first, the inline constructs applied outside
// code spans: **bold**, *italic*, [text](url). Group indexes follow the
// alternation order and are read via styleMdSpan. The italic alternative
// rejects a leading whitespace so bullet-like "* item" fragments never match.
var mdSpanRe = regexp.MustCompile(`\*\*([^*]+)\*\*|\*([^*\s][^*]*)\*|\[([^\]]*)\]\(([^)]*)\)`)

// mdATXRe recognizes ATX headings: 1-6 '#' characters followed by a space or
// end of line ("#hashtag" is plain text).
var mdATXRe = regexp.MustCompile(`^#{1,6}(?:\s|$)`)

// mdQuotePrefixRe matches a (possibly nested) blockquote prefix such as "> "
// or "  > > ".
var mdQuotePrefixRe = regexp.MustCompile(`^[ \t]*(?:>[ \t]?)+`)

// Markdown list-item markers: unordered bullets and ordered numbers, both
// followed by at least one space or tab.
var (
	mdBulletItemRe = regexp.MustCompile(`^[ \t]*[-*+][ \t]`)
	mdOrderedRe    = regexp.MustCompile(`^[ \t]*\d+[.)][ \t]`)
)

// mdSourceState tracks fenced-code state across consecutive Highlight calls.
// It is deliberately stateful: a fence opened on one line changes how every
// following line renders until it closes, which no per-line function can
// know. Callers rendering arbitrary windows must walk lines in document
// order from the top (or the last fence boundary) so state stays in sync.
type mdSourceState struct {
	inFence   bool
	fenceLang string
	fenceMark byte // '`' or '~': the closing fence must reuse the opener's char
}

// Highlight renders exactly one Markdown source line. Input and output are
// strictly 1:1 — never reflowed, merged, or wrapped (D1) — so file-review
// comment anchors stay aligned with real source lines. The input is
// sanitized before any styling; fence-open lines capture their info string
// as the inner-language for tools.HighlightLine (D2).
func (s *mdSourceState) Highlight(line string) string {
	line = ansi.Sanitize(line)
	if rendered, handled := s.highlightFenceLine(line); handled {
		return rendered
	}
	if s.inFence {
		return tools.HighlightLine(line, s.fenceLang)
	}
	return highlightMdBlock(line)
}

// highlightFenceLine classifies line as a fence marker. Openers and closers
// are rendered here with a colored marker and report handled=true; any other
// line reports handled=false and falls through. Inside a fence only a marker
// using the SAME character as the opener closes it, so ~~~ lines stay literal
// inside ``` fences. A closing marker may carry trailing text; it still
// closes — tolerance beats strict CommonMark here because hand-written docs
// routinely leave stray text on the closing fence.
func (s *mdSourceState) highlightFenceLine(line string) (string, bool) {
	t := strings.TrimLeft(line, " \t")
	mark, _, isMarker := splitMdFenceMark(t)
	if !isMarker {
		return "", false
	}
	if s.inFence {
		if mark[0] != s.fenceMark {
			return "", false // different fence char inside a fence: literal text
		}
		s.inFence = false
		s.fenceLang = ""
		return ansi.Fg(mdFenceColor) + line + ansi.FgReset, true
	}
	s.inFence = true
	s.fenceMark = mark[0]
	s.fenceLang = strings.TrimLeft(t[len(mark):], " \t")
	return ansi.Fg(mdFenceColor) + line + ansi.FgReset, true
}

// splitMdFenceMark splits a leading fence marker from its remainder.
// A marker is three or more backticks or tildes at the start of t (already
// left-trimmed); rest is everything after the marker run.
func splitMdFenceMark(t string) (mark, rest string, ok bool) {
	for _, m := range []string{"```", "~~~"} {
		if !strings.HasPrefix(t, m) {
			continue
		}
		i := len(m)
		for i < len(t) && t[i] == m[0] {
			i++
		}
		return t[:i], t[i:], true
	}
	return "", "", false
}

// highlightMdBlock styles one non-code Markdown line by dispatching to the
// first matching block construct: heading, blockquote, list item, or plain
// text with inline spans.
func highlightMdBlock(line string) string {
	if styled := highlightMdHeading(line); styled != "" {
		return styled
	}
	if styled := highlightMdQuote(line); styled != "" {
		return styled
	}
	if styled := highlightMdListItem(line); styled != "" {
		return styled
	}
	return highlightMdInline(line)
}

// highlightMdHeading renders ATX headings bold+colored. Headings are styled
// as one unit: inline spans would emit partial resets that strip the heading
// color mid-line, so span styling is intentionally skipped inside headings.
func highlightMdHeading(line string) string {
	t := strings.TrimLeft(line, " \t")
	if !mdATXRe.MatchString(t) {
		return ""
	}
	return ansi.Bold + ansi.Fg(mdHeadingColor) + line + ansi.BoldReset + ansi.FgReset
}

// highlightMdQuote colors the ">" markers of a blockquote prefix and
// inline-highlights the quoted content after them.
func highlightMdQuote(line string) string {
	loc := mdQuotePrefixRe.FindStringIndex(line)
	if loc == nil {
		return ""
	}
	prefix := ansi.Fg(mdQuoteColor) + line[loc[0]:loc[1]] + ansi.FgReset
	return prefix + highlightMdInline(line[loc[1]:])
}

// highlightMdListItem colors the bullet/number marker of a list item and
// inline-highlights the item text.
func highlightMdListItem(line string) string {
	re := mdBulletItemRe
	if !re.MatchString(line) {
		re = mdOrderedRe
		if !re.MatchString(line) {
			return ""
		}
	}
	loc := re.FindStringIndex(line)
	// The regex match includes the separator space/tab; the colored marker
	// ends before it so spacing stays untouched.
	markerEnd := strings.TrimRight(line[loc[0]:loc[1]], " \t")
	rest := line[loc[0]+len(markerEnd):]
	return ansi.Fg(mdBulletColor) + markerEnd + ansi.FgReset + highlightMdInline(rest)
}

// highlightMdInline splits on single-backtick code spans first — their
// content is literal and never span-formatted — then applies bold, italic,
// and link styling to the remaining segments. Delimiter characters are kept
// in the output: rendering is highlighted SOURCE (D1), every input byte stays
// visible. An unpaired trailing backtick leaves its tail segment unstyled
// rather than swallowing it as code.
func highlightMdInline(text string) string {
	parts := strings.Split(text, "`")
	var b strings.Builder
	for i, part := range parts {
		if i > 0 {
			b.WriteString("`") // keep the delimiter itself visible
		}
		b.WriteString(mdSegment(i, len(parts), part))
	}
	return b.String()
}

// mdSegment styles one backtick-delimited segment. Interior odd-index
// segments are code-span content; when the backtick count is odd the final
// segment (index n-1) is an unterminated tail and stays plain.
func mdSegment(i, n int, part string) string {
	if i%2 == 1 && (i != n-1 || n%2 == 1) {
		return ansi.Fg(mdCodeColor) + part + ansi.FgReset
	}
	return highlightMdSpans(part)
}

// highlightMdSpans applies **bold**, *italic*, and [text](url) styling in a
// single left-to-right pass. Matched regions are emitted styled and skipped,
// so replacements (which contain escape bytes) are never rescanned — this is
// what keeps SGR sequences like ESC[from ever matching the link pattern.
func highlightMdSpans(s string) string {
	var b strings.Builder
	last := 0
	for _, loc := range mdSpanRe.FindAllSubmatchIndex([]byte(s), -1) {
		b.WriteString(s[last:loc[0]])
		b.WriteString(styleMdSpan(s, loc))
		last = loc[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// styleMdSpan renders one matched span according to which alternative of
// mdSpanRe fired; group offsets follow the alternation order. Delimiters are
// re-emitted around the styled content — the output is highlighted source,
// never rewritten text (D1).
func styleMdSpan(s string, loc []int) string {
	switch {
	case loc[2] >= 0: // **bold**
		return ansi.Bold + "**" + s[loc[2]:loc[3]] + "**" + ansi.BoldReset
	case loc[4] >= 0: // *italic*
		return ansi.Italic + "*" + s[loc[4]:loc[5]] + "*" + ansi.ItalicReset
	default: // [text](url) — link text underlined+colored, URL kept visible (source fidelity) but faint
		text, url := s[loc[6]:loc[7]], s[loc[8]:loc[9]]
		return ansi.Underline + ansi.Fg(mdHeadingColor) + "[" + text + "]" +
			ansi.FgReset + ansi.UnderlineReset +
			ansi.Faint + "(" + url + ")" + ansi.FaintReset
	}
}
