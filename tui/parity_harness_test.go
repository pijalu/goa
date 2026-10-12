// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// The parity harness locks the *cell* fidelity of the emulator against a
// scripted session: the compositor renders a deterministic scene sequence, the
// resulting byte stream is replayed through TermEmulator, and the resulting
// cell grid (text + attributes + links + cursor + scrollback) is compared with
// a golden file.
//
// The goldens lock the emulator's cell model; the reference cross-check below
// (pyte, an independent xterm-spec emulator) proves the model agrees with
// another implementation on text, the five attribute booleans pyte models,
// and colours. pyte does not model SGR 2 (dim) or OSC 8 links, so those are
// outside the cross-check.
//
// Regenerate the goldens after an intentional emulator change:
//
//	GOA_UPDATE_GOLDEN=1 go test ./tui/ -run TestParity
const parityDir = "testdata/parity"

// parityCase is one scripted session: a sequence of compositor renders plus the
// terminal geometry, replayed verbatim.
type parityCase struct {
	name  string
	cols  int
	rows  int
	scene func(n int, cols int) *Scene
	// frames is how many times scene() is rendered, growing the transcript one
	// line at a time like a live stream.
	frames int
	// resizeTo, when non-zero, resizes the terminal before the final render.
	resizeTo int
	// tail is appended verbatim to the compositor's byte stream. The compositor
	// repaints the whole canvas instead of scrolling, so a case that must
	// exercise the emulator's scrollback feeds it the extra lines directly.
	tail string
}

// parityCases is the scripted corpus. Every line deliberately mixes the
// attributes the web UI must reproduce: bold, dim, italic, underline, inverse,
// strike, truecolor, palette, backgrounds, an OSC-8 link, CJK width handling,
// a scroll region, and a resize.
var parityCases = []parityCase{
	{
		name: "styled-stream", cols: 80, rows: 6, frames: 8,
		scene: func(n, cols int) *Scene {
			lines := make([]string, 0, n)
			for i := 0; i < n; i++ {
				lines = append(lines, styledLine(i, cols))
			}
			return &Scene{TerminalW: cols, TerminalH: 6, Layers: []Layer{{
				Name: "chat", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: cols, H: n},
				Content: lines,
			}}}
		},
	},
	{
		name: "styled-stream-resize", cols: 72, rows: 6, frames: 6, resizeTo: 90,
		scene: func(n, cols int) *Scene {
			lines := make([]string, 0, n)
			for i := 0; i < n; i++ {
				lines = append(lines, styledLine(i, cols))
			}
			return &Scene{TerminalW: cols, TerminalH: 6, Layers: []Layer{{
				Name: "chat", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: cols, H: n},
				Content: lines,
			}}}
		},
	},
	{
		// A stream longer than the screen: the lines overflow, so the golden
		// pins the scrollback rows (with attributes and links) too.
		name: "scrollback-stream", cols: 60, rows: 4, frames: 1,
		tail: "\x1b]8;;https://goa.dev\x1b\\linked\x1b]8;;\x1b\\ one\r\n" +
			"\x1b[1mbold two\x1b[0m\r\n" +
			"\x1b[38;5;208mpalette three\x1b[0m\r\n" +
			"\x1b[48;5;17m bg four\x1b[0m\r\n" +
			"\x1b[7minverse five\x1b[0m\r\n",
	},
}

// styledSegments is one transcript line: a cycle through every attribute the
// cell model tracks, so a scripted session exercises the whole matrix. Each
// entry is (SGR prefix, visible text).
var styledSegments = [][2]string{
	{"\x1b[1m", "bold"},
	{"\x1b[2m", "dim"},
	{"\x1b[3m", "italic"},
	{"\x1b[4m", "underline"},
	{"\x1b[7m", "inverse"},
	{"\x1b[9m", "strike"},
	{"\x1b[38;2;12;34;56m", "truecolor"},
	{"\x1b[38;5;208m", "palette"},
	{"\x1b[48;5;17m", " bg "},
	{"\x1b]8;;https://goa.dev\x1b\\", "goa"},
	{"\x1b]8;;\x1b\\", "plain"}, // closes the hyperlink
	{"", "日本語"},                 // double-width cells must occupy two columns each
}

// styledLine renders transcript line i, truncated to the terminal width on
// *visible* runes. Cutting on escape bytes instead would slice an SGR/OSC
// sequence in half and leave the terminal in a bogus attribute state, which is
// exactly the kind of corruption the harness must not bake into a golden.
func styledLine(i, cols int) string {
	var b strings.Builder
	used := 0
	for _, seg := range styledSegments {
		text := seg[1]
		if i > 0 {
			// Only the first segment carries the line number, so successive
			// renders really differ (the harness would otherwise compare
			// identical frames).
			text = strings.Replace(text, "bold", fmt.Sprintf("bold-%d", i), 1)
		}
		if cols > 0 && used+ansi.Width(text) > cols {
			break
		}
		if used > 0 {
			b.WriteByte(' ')
			used++
		}
		b.WriteString(seg[0])
		b.WriteString(text)
		if seg[0] != "" {
			b.WriteString("\x1b[0m")
		}
		used += ansi.Width(text)
	}
	if b.Len() > 0 {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// refColor converts an emulator SGR colour spec to the lowercase hex pyte
// reports ("default" for the terminal default), so both dumps are comparable.
func refColor(sgr string) string {
	if sgr == "" {
		return "default"
	}
	parts := strings.Split(sgr, ";")
	if len(parts) >= 5 && parts[1] == "2" {
		return fmt.Sprintf("%02x%02x%02x", atoiOr(parts[2], 0), atoiOr(parts[3], 0), atoiOr(parts[4], 0))
	}
	if len(parts) >= 3 && parts[1] == "5" {
		return refPalette256(atoiOr(parts[2], 0))
	}
	return "default"
}

func refPalette256(n int) string {
	if n < 16 {
		return refAnsi16[n]
	}
	levels := [6]int{0, 95, 135, 175, 215, 255}
	if n < 232 {
		i := n - 16
		return fmt.Sprintf("%02x%02x%02x", levels[i/36], levels[(i/6)%6], levels[i%6])
	}
	v := 8 + 10*(n-232)
	return fmt.Sprintf("%02x%02x%02x", v, v, v)
}

// refAnsi16 is the base palette pyte resolves indices 0-15 to.
var refAnsi16 = [16]string{
	"000000", "cd3131", "0dbc79", "e5e510",
	"2472c8", "bc3fbc", "11a8cd", "e5e5e5",
	"666666", "f14c4c", "23d18b", "f5f543",
	"3b8eea", "d670d6", "29b8db", "ffffff",
}

func atoiOr(s string, def int) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// refVisibleFlags are the attribute bits pyte actually models: dim and the
// link bit have no pyte counterpart, so they must not split a run here.
const refVisibleFlags = ^(AttrDim | AttrLink)

// refDump renders the emulator's screen in script/parity_ref.py's canonical
// format: runs of consecutive cells sharing bold/italic/underline/reverse/
// strike/fg/bg, with dim and links left out (pyte models neither).
func refDump(e *TermEmulator) string {
	w, h := e.Size()
	var b strings.Builder
	fmt.Fprintf(&b, "cols=%d rows=%d\n", w, h)
	for r := 0; r < h; r++ {
		cells := e.Cells(r)
		fmt.Fprintf(&b, "row=%d\n", r)
		var runText strings.Builder
		var run CellAttrs
		flush := func() {
			if runText.Len() == 0 {
				return
			}
			fmt.Fprintf(&b, "  %q b=%d i=%d u=%d r=%d s=%d fg=%s bg=%s\n",
				runText.String(),
				boolBit(run.Flags, AttrBold), boolBit(run.Flags, AttrItalic),
				boolBit(run.Flags, AttrUnderline), boolBit(run.Flags, AttrInverse),
				boolBit(run.Flags, AttrStrike),
				refColor(run.FG), refColor(run.BG))
			runText.Reset()
		}
		for _, c := range cells {
			// The link is deliberately not part of the grouping key: pyte does
			// not model OSC 8, so it merges a linked run with its plain
			// neighbours. Comparing the grouping as well as the content is the
			// point, and the only fair key is what both emulators model.
			if c.Text == "" {
				// pyte pre-fills the screen with spaces; Goa's emulator leaves
				// untouched cells empty. Normalise so both dumps agree.
				c.Text = " "
			}
			if runText.Len() > 0 && (c.Flags&refVisibleFlags != run.Flags&refVisibleFlags ||
				refColor(c.FG) != refColor(run.FG) || refColor(c.BG) != refColor(run.BG)) {
				flush()
			}
			run = c
			runText.WriteString(c.Text)
		}
		flush()
	}
	return b.String()
}

func boolBit(f, bit AttrFlags) int {
	if f&bit != 0 {
		return 1
	}
	return 0
}

// TestParity_MatchesReferenceEmulator replays each scripted session through
// pyte (script/parity_ref.py) and compares the resulting cells with Goa's own
// emulator. It is the cross-implementation proof that the cell model is
// faithful — the goldens only lock it against accidental change.
func TestParity_MatchesReferenceEmulator(t *testing.T) {
	if testing.Short() {
		t.Skip("parity harness skipped in short mode")
	}
	for _, c := range parityCases {
		t.Run(c.name, func(t *testing.T) {
			bytes := parityBytes(t, c)
			path := filepath.Join(t.TempDir(), "stream.bin")
			if err := os.WriteFile(path, []byte(bytes), 0o644); err != nil {
				t.Fatalf("write stream: %v", err)
			}
			cols := parityCols(c)
			cmd := exec.Command("python3", "../script/parity_ref.py", path,
				fmt.Sprint(cols), fmt.Sprint(c.rows))
			out, err := cmd.Output()
			if err != nil {
				if _, ok := err.(*exec.ExitError); ok {
					t.Skip("python3/pyte unavailable — reference cross-check skipped")
				}
				t.Skipf("cannot run python3: %v", err)
			}
			emu := NewTermEmulator(c.rows, cols)
			emu.Process(bytes)
			got := refDump(emu)
			if got != string(out) {
				t.Errorf("emulator cells diverge from the pyte reference:\n--- goa ---\n%s\n--- pyte ---\n%s",
					got, out)
			}
		})
	}
}

// parityBytes is the raw byte stream a scripted session produces.
func parityBytes(t *testing.T, c parityCase) string {
	t.Helper()
	term := &fakeTerminal{w: c.cols, h: c.rows}
	if c.scene != nil {
		comp := NewCompositor(term)
		for n := 1; n <= c.frames; n++ {
			comp.Render(c.scene(n, c.cols))
		}
		if c.resizeTo > 0 {
			term.w = c.resizeTo
			comp.Render(c.scene(c.frames, c.resizeTo))
		}
	}
	return strings.Join(term.Writes(), "") + c.tail
}

// parityCols is the terminal width the session ends at (the resize case ends
// wider than it started).
func parityCols(c parityCase) int {
	if c.resizeTo > 0 {
		return c.resizeTo
	}
	return c.cols
}

// runParity replays a scripted session through the compositor and the emulator,
// returning the canonical cell dump.
func runParity(t *testing.T, c parityCase) string {
	t.Helper()
	emu := NewTermEmulator(c.rows, parityCols(c))
	emu.TrackDirty(true)
	emu.Process(parityBytes(t, c))
	emu.DrainDirty()
	return dumpCells(emu)
}

// dumpCells renders the emulator's screen (and scrollback) as a canonical,
// line-oriented text form. Every attribute is spelled out, so a golden diff
// says exactly which cell changed rather than just "the screen differs".
func dumpCells(e *TermEmulator) string {
	w, h := e.Size()
	var b strings.Builder
	fmt.Fprintf(&b, "cols=%d rows=%d\n", w, h)
	cr, cc := e.Cursor()
	fmt.Fprintf(&b, "cursor=%d,%d\n", cr, cc)
	fmt.Fprintf(&b, "scrollback=%d\n", len(e.ScrollbackCells()))
	for r := 0; r < h; r++ {
		cells := e.Cells(r)
		runs := cellRuns(cells)
		fmt.Fprintf(&b, "row=%d runs=%d\n", r, len(runs))
		for _, run := range runs {
			fmt.Fprintf(&b, "  %s\n", run)
		}
	}
	for i, row := range e.ScrollbackCells() {
		runs := cellRuns(row)
		fmt.Fprintf(&b, "sb=%d runs=%d\n", i, len(runs))
		for _, run := range runs {
			fmt.Fprintf(&b, "  %s\n", run)
		}
	}
	return b.String()
}

// cellRuns collapses a row into "text|flags|fg|bg|link" segments, skipping
// default-attribute cells.
func cellRuns(cells []CellAttrs) []string {
	var out []string
	var curText strings.Builder
	var curFlags AttrFlags
	var curFG, curBG, curLink string
	flush := func() {
		if curText.Len() == 0 {
			return
		}
		out = append(out, fmt.Sprintf("%q flags=%s fg=%s bg=%s link=%s",
			curText.String(), curFlags, curFG, curBG, curLink))
	}
	for _, c := range cells {
		if c.Flags == 0 && c.FG == "" && c.BG == "" && c.Link == "" {
			flush()
			curText.Reset()
			curFlags, curFG, curBG, curLink = 0, "", "", ""
			continue
		}
		if curText.Len() > 0 && (c.Flags != curFlags || c.FG != curFG ||
			c.BG != curBG || c.Link != curLink) {
			flush()
			curText.Reset()
		}
		curFlags, curFG, curBG, curLink = c.Flags, c.FG, c.BG, c.Link
		curText.WriteString(c.Text)
	}
	flush()
	return out
}

func TestParity_ScriptedSessionMatchesGolden(t *testing.T) {
	if testing.Short() {
		t.Skip("parity harness skipped in short mode")
	}
	for _, c := range parityCases {
		t.Run(c.name, func(t *testing.T) {
			checkParityGolden(t, c)
		})
	}
}

// checkParityGolden compares one scripted session's cell dump with its golden,
// writing the golden instead when GOA_UPDATE_GOLDEN=1 asks for a refresh.
func checkParityGolden(t *testing.T, c parityCase) {
	t.Helper()
	got := runParity(t, c)
	path := filepath.Join(parityDir, c.name+".golden")
	if os.Getenv("GOA_UPDATE_GOLDEN") == "1" {
		writeParityGolden(t, path, got)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with GOA_UPDATE_GOLDEN=1 to create): %v", err)
	}
	if got != string(want) {
		t.Errorf("cell grid diverged from the golden:\n--- got ---\n%s\n--- want ---\n%s",
			got, want)
	}
}

// writeParityGolden (re)creates one golden file.
func writeParityGolden(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(parityDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write golden: %v", err)
	}
	t.Logf("wrote %s", path)
}

// The dump must be stable: two runs of the same scripted session must produce
// byte-identical output, or the goldens themselves would be noise.
func TestParity_DumpIsDeterministic(t *testing.T) {
	for _, c := range parityCases {
		if a, b := runParity(t, c), runParity(t, c); a != b {
			t.Errorf("%s: two runs differ", c.name)
		}
	}
}

// The cell model must observe every attribute the compositor emits — a silent
// loss here is exactly the drift the harness exists to catch.
func TestParity_ScriptedSessionExercisesEveryAttribute(t *testing.T) {
	emu := NewTermEmulator(3, 200)
	emu.Process(styledLine(0, 0))
	seen := map[string]bool{}
	for _, cell := range emu.Cells(0) {
		for _, name := range []struct {
			bit  AttrFlags
			name string
		}{
			{AttrBold, "bold"}, {AttrDim, "dim"}, {AttrItalic, "italic"},
			{AttrUnderline, "underline"}, {AttrInverse, "inverse"},
			{AttrStrike, "strike"}, {AttrLink, "link"},
		} {
			if cell.Flags&name.bit != 0 {
				seen[name.name] = true
			}
		}
		if cell.FG != "" {
			seen["fg:"+cell.FG] = true
		}
		if cell.BG != "" {
			seen["bg"] = true
		}
	}
	for _, want := range []string{"bold", "dim", "italic", "underline",
		"inverse", "strike", "link", "fg:38;2;12;34;56", "fg:38;5;208", "bg"} {
		if !seen[want] {
			t.Errorf("scripted session never produced %q", want)
		}
	}
}
