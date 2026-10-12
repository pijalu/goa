// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"strconv"
	"strings"

	"github.com/pijalu/goa/tui"
)

// BlockOp is one wire operation on a conversation block (specs/webui.md §22).
// A client keys its DOM on ID and applies ops in order.
type BlockOp struct {
	// Op is "set" (create or replace), "reset" (drop every block — history
	// was compressed or cleared; the sets that follow rebuild it), or "del".
	Op string `json:"op"`
	// ID is the stable block id (tui.HeaderBlockID for the header art).
	ID int `json:"id"`
	// Kind is the block kind; empty on a del.
	Kind string `json:"kind,omitempty"`
	// Text is the block's source content. With From > 0 it is only the
	// SUFFIX pasted after the text the client already holds (a streaming
	// block strictly growing), so a long answer costs one append, not a
	// re-ship of everything before it.
	Text string `json:"text,omitempty"`
	// From is the byte offset Text starts at in the client's current text.
	// 0 on a full set.
	From int `json:"from,omitempty"`
	// Meta carries kind-specific facts (tool, args, status, duration,
	// expanded, agent, companion). Only present when it changed.
	Meta map[string]string `json:"meta,omitempty"`
	// Runs are the pre-styled spans for art blocks (the header). Only
	// present when the art changed.
	Runs []Run `json:"runs,omitempty"`
}

// blockState is the tracker's record of the block a client last received.
type blockState struct {
	kind string
	text string
	meta map[string]string
	runs []Run
}

// BlockTracker turns Scene block snapshots into the wire ops a client needs
// to mirror the conversation. It is the blocks-plane counterpart of the cell
// grid's diff baseline: it holds what clients last received, keeps the full
// journal (so a freshly attached browser is answered with band + journal,
// the way the cells plane answers with FullFrame + scrollback), and detects
// rewrites (compression, /clear) as a reset.
type BlockTracker struct {
	prev     map[int]blockState
	ids      []int // current ids, in order
	replaced bool  // pending: client must drop its blocks and rebuild
}

// NewBlockTracker returns a tracker with no client state.
func NewBlockTracker() *BlockTracker {
	return &BlockTracker{prev: map[int]blockState{}}
}

// ResetForgets, on the next Sync, tells the client to drop everything and
// rebuild from the journal the following Sync carries. Used when a session
// rotates under the page.
func (t *BlockTracker) ResetForgets() {
	t.prev = map[int]blockState{}
	t.ids = nil
	t.replaced = true
}

// Sync diffs one Scene snapshot into ops. The returned ops are the smallest
// set that brings a client holding the previous snapshot to this one; a nil
// result means nothing changed. A snapshot that lost blocks (a compression
// collapsed the history) is answered with a reset: ops rebuilding the whole
// journal, flagged so the client clears first.
func (t *BlockTracker) Sync(blocks []tui.SceneBlock) []BlockOp {
	// Lost blocks mean the conversation the client mirrors no longer exists
	// in that form: rebuild from scratch.
	kept := make(map[int]struct{}, len(blocks))
	for _, b := range blocks {
		kept[b.ID] = struct{}{}
	}
	rewrite := t.replaced
	for _, id := range t.ids {
		if _, ok := kept[id]; !ok {
			rewrite = true
			break
		}
	}

	var ops []BlockOp
	if rewrite {
		t.prev = map[int]blockState{}
		t.ids = t.ids[:0]
		t.replaced = false
		ops = append(ops, BlockOp{Op: "reset"})
		for _, b := range blocks {
			ops = append(ops, t.set(b)...)
		}
		return ops
	}

	for _, b := range blocks {
		if op, changed := t.diff(b); changed {
			ops = append(ops, op)
		}
	}
	return ops
}

// Journal returns ops that rebuild the whole conversation for a newly
// attached client (which holds nothing yet).
func (t *BlockTracker) Journal() []BlockOp {
	ops := make([]BlockOp, 0, len(t.ids)+1)
	ops = append(ops, BlockOp{Op: "reset"})
	for _, id := range t.ids {
		st := t.prev[id]
		ops = append(ops, BlockOp{Op: "set", ID: id, Kind: st.kind, Text: st.text, Meta: st.meta, Runs: st.runs})
	}
	return ops
}

// diff produces the op for one block, reporting whether anything changed.
func (t *BlockTracker) diff(b tui.SceneBlock) (BlockOp, bool) {
	st := blockState{kind: string(b.Kind), text: b.Text, meta: b.Meta}
	if len(b.Lines) > 0 {
		st.runs = StyledLinesToRuns(b.Lines)
	}
	old, seen := t.prev[b.ID]
	if seen && old.kind == st.kind && sameMeta(old.meta, st.meta) && sameRuns(old.runs, st.runs) {
		if old.text == st.text {
			return BlockOp{}, false
		}
		if strings.HasPrefix(st.text, old.text) {
			// Streaming growth: ship only the suffix.
			op := BlockOp{Op: "set", ID: b.ID, From: len(old.text), Text: st.text[len(old.text):]}
			t.prev[b.ID] = st
			return op, true
		}
	}
	t.prev[b.ID] = st
	if !seen {
		t.ids = append(t.ids, b.ID)
	}
	return BlockOp{Op: "set", ID: b.ID, Kind: st.kind, Text: st.text, Meta: st.meta, Runs: st.runs}, true
}

// set unconditionally records and emits a full set (reset path).
func (t *BlockTracker) set(b tui.SceneBlock) []BlockOp {
	st := blockState{kind: string(b.Kind), text: b.Text, meta: b.Meta}
	if len(b.Lines) > 0 {
		st.runs = StyledLinesToRuns(b.Lines)
	}
	t.prev[b.ID] = st
	t.ids = append(t.ids, b.ID)
	return []BlockOp{{Op: "set", ID: b.ID, Kind: st.kind, Text: st.text, Meta: st.meta, Runs: st.runs}}
}

// sameMeta compares two meta maps (nil == empty).
func sameMeta(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// sameRuns compares two run slices.
func sameRuns(a, b []Run) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// StyledLinesToRuns converts styled lines (SGR allowed — the same convention
// as Layer.Content) into the runs the wire carries. Cursor-positioning
// escapes have no business in content lines and are stripped; OSC-8
// hyperlinks are honoured. SGR state carries across lines within one call
// (art blocks are one visual unit), starting from the default style.
func StyledLinesToRuns(lines []string) []Run {
	var runs []Run
	st := sgrState{}
	for i, line := range lines {
		if i > 0 {
			runs = append(runs, Run{Text: "\n"})
		}
		runs = st.emit(line, runs)
	}
	return runs
}

// sgrState is the running SGR style while converting styled text to runs.
type sgrState struct {
	flags uint16
	fg    string
	bg    string
	link  string
}

// emit converts one line, appending to runs, advancing the SGR state. The
// run is flushed BEFORE a sequence applies — the state change must not leak
// into the text before it.
func (st *sgrState) emit(line string, runs []Run) []Run {
	var text strings.Builder
	flush := func() {
		if text.Len() == 0 {
			return
		}
		runs = append(runs, Run{Text: text.String(), Flags: tui.AttrFlags(st.flags), FG: st.fg, BG: st.bg, Link: st.link})
		text.Reset()
	}
	for i := 0; i < len(line); {
		c := line[i]
		if c != 0x1b {
			text.WriteByte(c)
			i++
			continue
		}
		seq, n := ansiNextSequence(line[i:])
		if n == 0 {
			// Incomplete escape at end of line: drop it (a lone ESC is
			// never content).
			i++
			continue
		}
		flush()
		st.apply(string(seq))
		i += n
	}
	flush()
	return runs
}

// apply updates the state from one escape sequence. SGR sequences mutate the
// style; OSC-8 sets/clears the link; everything else is ignored.
func (st *sgrState) apply(seq string) {
	switch {
	case strings.HasPrefix(seq, "\x1b[") && strings.HasSuffix(seq, "m"):
		st.applySGR(strings.TrimSuffix(strings.TrimPrefix(seq, "\x1b["), "m"))
	case strings.HasPrefix(seq, "\x1b]8;"):
		st.applyOSC8(seq)
	}
}

// applySGR parses the parameters of one SGR sequence, dispatching each code
// to the simple-flag or extended-colour handler.
func (st *sgrState) applySGR(params string) {
	if params == "" {
		params = "0"
	}
	fields := strings.Split(params, ";")
	for i := 0; i < len(fields); i++ {
		n, err := strconv.Atoi(fields[i])
		if err != nil {
			continue
		}
		if n == 38 || n == 48 {
			used := st.applyColor(n, fields[i+1:])
			if used == 0 {
				return
			}
			i += used
			continue
		}
		st.applyFlagCode(n)
	}
}

// applyColor consumes an extended-colour spec (38;5;n or 38;2;r;g;b) after
// the selector and applies it, returning the fields consumed (0 = malformed;
// the caller stops — the sequence is truncated).
func (st *sgrState) applyColor(n int, rest []string) int {
	spec, used := sgrColor(rest)
	if used == 0 {
		return 0
	}
	// sgrToCSS parses the FULL spec ("38;5;n" / "38;2;r;g;b"): it cuts the
	// channel off itself.
	css := sgrToCSS(strconv.Itoa(n) + ";" + strings.Join(spec, ";"))
	if n == 38 {
		st.fg = css
	} else {
		st.bg = css
	}
	return used
}

// sgrFlagBits maps a simple SGR code to the attribute flags it sets (>0) or
// clears (clear != 0 clears exactly those bits; code 0 is the full reset).
var sgrFlagBits = map[int]struct{ set, clear uint16 }{
	1:  {set: 1},
	2:  {set: 8}, // dim
	3:  {set: 2},
	4:  {set: 4},
	7:  {set: 16},
	9:  {set: 32},
	22: {clear: 1 | 8},
	23: {clear: 2},
	24: {clear: 4},
	27: {clear: 16},
	29: {clear: 32},
}

// applyFlagCode applies one simple SGR code (style flags and default
// colours).
func (st *sgrState) applyFlagCode(n int) {
	switch n {
	case 0:
		st.flags, st.fg, st.bg, st.link = 0, "", "", ""
	case 39:
		st.fg = ""
	case 49:
		st.bg = ""
	default:
		if bit, ok := sgrFlagBits[n]; ok {
			st.flags = (st.flags &^ bit.clear) | bit.set
		}
	}
}

// applyOSC8 handles an OSC 8 hyperlink: \x1b]8;params;URI\x1b\\ (or BEL).
// An empty URI closes the link.
func (st *sgrState) applyOSC8(seq string) {
	body := strings.TrimPrefix(seq, "\x1b]8;")
	terminator := ""
	for _, t := range []string{"\x1b\\", "\x07"} {
		if idx := strings.Index(body, t); idx >= 0 {
			terminator = body[:idx]
			break
		}
	}
	if terminator == "" {
		return
	}
	parts := strings.SplitN(terminator, ";", 2)
	if len(parts) == 2 && parts[1] != "" {
		st.link = parts[1]
		st.flags |= 4
		return
	}
	st.link = ""
	st.flags &^= 4
}

// ansiNextSequence reports the escape sequence at the front of buf and its
// byte length. It recognises the two sequence families that legitimately
// appear in content lines — CSI (ESC [ … final byte) and OSC (ESC ] …
// terminated by BEL or ST) — plus any other two-byte ESC sequence. A length
// of 0 means "incomplete" (no complete sequence at the front).
func ansiNextSequence(buf string) (string, int) {
	if len(buf) < 2 || buf[0] != 0x1b {
		return "", 0
	}
	switch buf[1] {
	case '[':
		return nextCSISeq(buf)
	case ']':
		return nextOSCSeq(buf)
	default:
		return buf[:2], 2
	}
}

// nextCSISeq scans a CSI sequence: ESC [ … final byte (0x40–0x7e).
func nextCSISeq(buf string) (string, int) {
	for i := 2; i < len(buf); i++ {
		if buf[i] >= 0x40 && buf[i] <= 0x7e {
			return buf[:i+1], i + 1
		}
	}
	return "", 0
}

// nextOSCSeq scans an OSC sequence: ESC ] … terminated by BEL or ST (ESC \).
func nextOSCSeq(buf string) (string, int) {
	for i := 2; i+1 < len(buf); i++ {
		if buf[i] == 0x07 {
			return buf[:i+1], i + 1
		}
		if buf[i] == 0x1b && buf[i+1] == '\\' {
			return buf[:i+2], i + 2
		}
	}
	return "", 0
}

// sgrColor consumes an extended-colour spec (38;5;n or 38;2;r;g;b) from the
// front of fields, returning the consumed fields including the leading
// selector.
func sgrColor(fields []string) ([]string, int) {
	if len(fields) == 0 {
		return nil, 0
	}
	switch fields[0] {
	case "5":
		if len(fields) < 2 {
			return nil, 0
		}
		return fields[:2], 2
	case "2":
		if len(fields) < 5 {
			return nil, 0
		}
		return fields[:5], 5
	}
	return nil, 0
}
