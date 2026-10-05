// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"testing"

	"github.com/pijalu/goa/tui"
)

// ─────────────────────────────────────────────────────────────── BlockTracker

func sceneBlock(id int, kind tui.SceneBlockKind, text string) tui.SceneBlock {
	return tui.SceneBlock{ID: id, Kind: kind, Text: text}
}

// TestBlockTracker_SetAppendReset walks a conversation through creation and
// streaming growth, asserting the ops each stage produces. A fresh Sync
// emits plain sets — the reset+set journal is what an ATTACH receives
// (BlockTracker.Journal); a client's baseline is always established there
// first.
func TestBlockTracker_SetAppendReset(t *testing.T) {
	tr := NewBlockTracker()

	// First snapshot: two blocks, two sets.
	ops := tr.Sync([]tui.SceneBlock{
		sceneBlock(1, tui.BlockUser, "hi"),
		sceneBlock(2, tui.BlockAssistant, "hel"),
	})
	if len(ops) != 2 || ops[0].ID != 1 || ops[1].ID != 2 {
		t.Fatalf("first sync ops = %+v, want 2 sets", ops)
	}

	// No change: no ops.
	if ops := tr.Sync([]tui.SceneBlock{
		sceneBlock(1, tui.BlockUser, "hi"),
		sceneBlock(2, tui.BlockAssistant, "hel"),
	}); ops != nil {
		t.Fatalf("unchanged sync produced ops: %+v", ops)
	}

	// Streaming growth: one append op carrying only the suffix.
	ops = tr.Sync([]tui.SceneBlock{
		sceneBlock(1, tui.BlockUser, "hi"),
		sceneBlock(2, tui.BlockAssistant, "hello world"),
	})
	if len(ops) != 1 || ops[0].From != 3 || ops[0].Text != "lo world" {
		t.Fatalf("append ops = %+v, want one From=3 suffix", ops)
	}
}

// TestBlockTracker_RewriteAndMeta asserts in-place rewrites ship a full set,
// with and without metadata changes.
func TestBlockTracker_RewriteAndMeta(t *testing.T) {
	tr := NewBlockTracker()
	tr.Sync([]tui.SceneBlock{
		sceneBlock(1, tui.BlockUser, "hi"),
		sceneBlock(2, tui.BlockAssistant, "hello world"),
	})

	// In-place rewrite (not a prefix): full set.
	ops := tr.Sync([]tui.SceneBlock{
		sceneBlock(1, tui.BlockUser, "hi"),
		sceneBlock(2, tui.BlockAssistant, "completely different"),
	})
	if len(ops) != 1 || ops[0].From != 0 || ops[0].Text != "completely different" {
		t.Fatalf("rewrite ops = %+v, want one full set", ops)
	}

	// Metadata change: full set with the new meta.
	ops = tr.Sync([]tui.SceneBlock{
		sceneBlock(1, tui.BlockUser, "hi"),
		{ID: 2, Kind: tui.BlockTool, Text: "completely different", Meta: map[string]string{"status": "success"}},
	})
	if len(ops) != 1 || ops[0].Meta["status"] != "success" {
		t.Fatalf("meta ops = %+v, want one set carrying status", ops)
	}
}

// TestBlockTracker_ResetAndJournal asserts lost blocks (a compression or a
// clear) produce a reset + rebuild, and that the journal rebuilds everything
// for a fresh client.
func TestBlockTracker_ResetAndJournal(t *testing.T) {
	tr := NewBlockTracker()
	tr.Sync([]tui.SceneBlock{sceneBlock(1, tui.BlockUser, "hi")})

	ops := tr.Sync([]tui.SceneBlock{sceneBlock(3, tui.BlockUser, "fresh")})
	if len(ops) != 2 || ops[0].Op != "reset" || ops[1].ID != 3 {
		t.Fatalf("reset ops = %+v, want reset + set(3)", ops)
	}

	journal := tr.Journal()
	if len(journal) != 2 || journal[0].Op != "reset" || journal[1].ID != 3 {
		t.Fatalf("journal = %+v, want reset + set(3)", journal)
	}
}

// TestBlockTracker_ClearToEmpty asserts a cleared conversation yields a bare
// reset (and does not panic).
func TestBlockTracker_ClearToEmpty(t *testing.T) {
	tr := NewBlockTracker()
	tr.Sync([]tui.SceneBlock{sceneBlock(1, tui.BlockUser, "hi")})
	ops := tr.Sync(nil)
	if len(ops) != 1 || ops[0].Op != "reset" {
		t.Fatalf("clear ops = %+v, want one reset", ops)
	}
	if ops := tr.Sync(nil); ops != nil {
		t.Fatalf("second clear ops = %+v, want none", ops)
	}
}

// ─────────────────────────────────────────────────────── StyledLinesToRuns

func runTexts(runs []Run) []string {
	out := make([]string, len(runs))
	for i, r := range runs {
		out[i] = r.Text
	}
	return out
}

// TestStyledLinesToRuns_SGR covers style runs, style carry-over, resets, and
// 256-colour mapping.
func TestStyledLinesToRuns_SGR(t *testing.T) {
	runs := StyledLinesToRuns([]string{
		"\x1b[1mbold\x1b[0m plain",
		"\x1b[38;5;80mblue\x1b[m rest",
	})
	want := []Run{
		{Text: "bold", Flags: 1},
		{Text: " plain"},
		{Text: "\n"},
		{Text: "blue", FG: "#5fd7d7"}, // 256-colour 80: the mascot cyan
		{Text: " rest"},
	}
	if len(runs) != len(want) {
		t.Fatalf("runs = %+v, want %+v", runs, want)
	}
	for i := range want {
		if runs[i] != want[i] {
			t.Errorf("run %d = %+v, want %+v", i, runs[i], want[i])
		}
	}
	if got := runTexts(StyledLinesToRuns([]string{"a", "b"})); len(got) != 3 || got[0] != "a" || got[1] != "\n" || got[2] != "b" {
		t.Errorf("multi-line runs = %q, want [a \\n b]", got)
	}
}

// TestStyledLinesToRuns_OSC8 covers hyperlink capture and clearing.
func TestStyledLinesToRuns_OSC8(t *testing.T) {
	runs := StyledLinesToRuns([]string{
		"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\ tail",
	})
	if len(runs) != 2 {
		t.Fatalf("runs = %+v, want 2", runs)
	}
	if runs[0].Link != "https://example.com" || runs[0].Text != "link" {
		t.Errorf("run 0 = %+v, want the link run", runs[0])
	}
	if runs[1].Link != "" || runs[1].Text != " tail" {
		t.Errorf("run 1 = %+v, want plain", runs[1])
	}
}

// TestStyledLinesToRuns_IncompleteEscape asserts a dangling ESC never eats
// visible text.
func TestStyledLinesToRuns_IncompleteEscape(t *testing.T) {
	runs := StyledLinesToRuns([]string{"text\x1b"})
	if len(runs) != 1 || runs[0].Text != "text" {
		t.Fatalf("runs = %+v, want [text]", runs)
	}
}

// ──────────────────────────────────────────────────────── plane plumbing

func patchRows(patches []RowPatch) []int {
	out := make([]int, len(patches))
	for i, p := range patches {
		out[i] = p.Row
	}
	return out
}

func minRow(rows []int) int {
	m := -1
	for i, r := range rows {
		if i == 0 || r < m {
			m = r
		}
	}
	return m
}

// frameRecorder keeps every published frame in order.
type frameRecorder struct {
	frames []*Frame
}

func (r *frameRecorder) Publish(f *Frame) { r.frames = append(r.frames, f) }
func (r *frameRecorder) HasClients() bool { return true }

// TestVirtualTerminal_BlocksPlanePublish drives a blocks-plane terminal
// through ObserveScene + publish: band-only shipping with the popup seated
// on the band, the full-cell overlay fallback, and the restore.
func TestVirtualTerminal_BlocksPlanePublish(t *testing.T) {
	vt := NewVirtualTerminal(20, 10)
	vt.SetPlane(PlaneBlocks)
	rec := &frameRecorder{}
	vt.SetSink(rec)

	t.Run("band only", func(t *testing.T) { publishBandStage(t, vt, rec) })
	t.Run("overlay fallback", func(t *testing.T) { publishOverlayStage(t, vt, rec) })
	t.Run("overlay restore", func(t *testing.T) { publishRestoreStage(t, vt, rec) })
}

// publishBandStage ships a scene with chrome + a non-capturing popup and
// asserts the band-only frame.
func publishBandStage(t *testing.T, vt *VirtualTerminal, rec *frameRecorder) {
	t.Helper()
	// The scene: 2 chrome rows, one non-capturing popup overlay seated on
	// the band (2 rows tall), one conversation block.
	vt.ObserveScene(&tui.Scene{
		TerminalW:    20,
		TerminalH:    10,
		ChromeHeight: 2,
		Blocks:       []tui.SceneBlock{sceneBlock(1, tui.BlockUser, "hi")},
		Layers: []tui.Layer{
			{Kind: tui.LayerOverlay, Rect: tui.Rect{Y: 6, H: 2}, Content: []string{"pop", "up"}},
		},
	})
	// Fill all ten rows so the band rows themselves are dirty. Early frames
	// legitimately carry no band patches (the band is not dirty yet) — the
	// assertions below target the first frame that has any.
	for i := 0; i < 10; i++ {
		vt.WriteString("band filler\r\n")
	}
	if len(rec.frames) == 0 {
		t.Fatal("no frame published")
	}
	// The block ops ride the FIRST frame after the scene was observed…
	if first := rec.frames[0]; len(first.Blocks) != 1 || first.Blocks[0].ID != 1 || first.Blocks[0].Op != "set" {
		t.Errorf("frame.Blocks = %+v, want one set(1)", first.Blocks)
	}
	// …while the band assertions target the first frame with patches.
	frame := firstFrameWithPatches(rec.frames)
	if frame == nil {
		t.Fatal("no frame with band patches published")
	}
	if frame.Chrome != 4 { // 2 chrome + 2 popup rows
		t.Errorf("frame.Chrome = %d, want 4", frame.Chrome)
	}
	if frame.Overlay {
		t.Error("frame.Overlay = true, want false for a non-capturing popup")
	}
	if minRow(patchRows(frame.Patches)) < 10-4 {
		t.Errorf("band frame ships rows above the band: %v", patchRows(frame.Patches))
	}
}

// publishOverlayStage raises an input-capturing overlay and asserts the
// full-cell flip.
func publishOverlayStage(t *testing.T, vt *VirtualTerminal, rec *frameRecorder) {
	t.Helper()
	before := len(rec.frames)
	vt.ObserveScene(&tui.Scene{
		TerminalW:            20,
		TerminalH:            10,
		ChromeHeight:         2,
		OverlayCapturesInput: true,
	})
	vt.HideCursor()
	var overlayFrame *Frame
	for _, f := range rec.frames[before:] {
		if f.Overlay {
			overlayFrame = f
		}
	}
	if overlayFrame == nil {
		t.Fatal("no overlay frame published")
	}
	if rows := patchRows(overlayFrame.Patches); len(rows) == 0 || minRow(rows) != 0 {
		t.Errorf("overlay frame rows = %v, want the full grid from row 0", rows)
	}
}

// publishRestoreStage closes the overlay — the engine re-observes a
// non-capturing scene, exactly as the next rendered frame would — and
// asserts the full band frame that rebuilds the page's footer.
func publishRestoreStage(t *testing.T, vt *VirtualTerminal, rec *frameRecorder) {
	t.Helper()
	vt.ObserveScene(&tui.Scene{
		TerminalW:    20,
		TerminalH:    10,
		ChromeHeight: 2,
	})
	vt.ShowCursor()
	var after *Frame
	for i := len(rec.frames) - 1; i >= 0; i-- {
		if !rec.frames[i].Overlay {
			after = rec.frames[i]
			break
		}
	}
	if after == nil || after.Overlay {
		t.Fatalf("post-overlay frame = %+v, want Overlay=false", after)
	}
	if !after.Full {
		t.Error("post-overlay frame is not full; the client could not rebuild the band")
	}
}

// firstFrameWithPatches returns the first frame carrying row patches.
func firstFrameWithPatches(frames []*Frame) *Frame {
	for _, f := range frames {
		if len(f.Patches) > 0 {
			return f
		}
	}
	return nil
}

// TestVirtualTerminal_CellsPlaneUnchanged asserts the zero-value plane keeps
// the v1 behaviour: whole-grid frames with scrollback batches.
func TestVirtualTerminal_CellsPlaneUnchanged(t *testing.T) {
	vt := NewVirtualTerminal(20, 10)
	rec := &frameRecorder{}
	vt.SetSink(rec)
	// Fill the screen twice so rows scroll into the scrollback.
	for i := 0; i < 25; i++ {
		vt.WriteString("line\r\n")
	}
	if len(rec.frames) == 0 {
		t.Fatal("no frames")
	}
	var scrolled bool
	for _, f := range rec.frames {
		if f.Chrome != 0 || f.Overlay || f.Blocks != nil {
			t.Fatalf("cells frame = %+v, want no blocks-plane fields", f)
		}
		if f.Scrollback != nil {
			scrolled = true
		}
	}
	if !scrolled {
		t.Error("no cells frame carried a scrollback batch")
	}
	attach := vt.AttachFrame()
	if len(attach.Patches) != 10 {
		t.Errorf("cells attach = %d patches, want the whole 10-row grid", len(attach.Patches))
	}
}

// TestVirtualTerminal_BlocksAttachFrame asserts a fresh blocks-plane client
// receives band patches + the full journal (reset + sets), and that the
// cursor field stays grid-absolute (the client offsets it by chrome).
func TestVirtualTerminal_BlocksAttachFrame(t *testing.T) {
	vt := NewVirtualTerminal(20, 10)
	vt.SetPlane(PlaneBlocks)
	vt.ObserveScene(&tui.Scene{
		TerminalW:    20,
		TerminalH:    10,
		ChromeHeight: 2,
		Blocks:       []tui.SceneBlock{sceneBlock(7, tui.BlockSystem, "note")},
	})
	vt.WriteString("band row")
	f := vt.AttachFrame()
	if !f.Full || f.Chrome != 2 {
		t.Fatalf("attach frame = full=%v chrome=%d, want full band of 2", f.Full, f.Chrome)
	}
	if len(f.Blocks) != 2 || f.Blocks[0].Op != "reset" || f.Blocks[1].ID != 7 || f.Blocks[1].Text != "note" {
		t.Errorf("attach blocks = %+v, want reset + set(7)", f.Blocks)
	}
	for _, p := range f.Patches {
		if p.Row < 10-2 {
			t.Fatalf("attach ships transcript row %d in the blocks plane", p.Row)
		}
	}
}

// TestFrameCodec_BlocksRoundTrip locks the wire shape of the blocks-plane
// frame fields (chrome, overlay, full).
func TestFrameCodec_BlocksRoundTrip(t *testing.T) {
	f := &Frame{Seq: 9, Cols: 80, Rows: 24, Chrome: 4, Overlay: true, Full: true}
	got := encodeDecodeFrame(t, f)
	if got.Chrome != 4 || !got.Overlay || !got.Full || got.Seq != 9 {
		t.Errorf("decoded frame = chrome %d overlay %v full %v seq %d", got.Chrome, got.Overlay, got.Full, got.Seq)
	}
}

// TestFrameCodec_BlocksOpRoundTrip locks the wire shape of one block op.
func TestFrameCodec_BlocksOpRoundTrip(t *testing.T) {
	f := &Frame{Seq: 1, Blocks: []BlockOp{
		{Op: "reset"},
		{Op: "set", ID: 3, Kind: "tool", From: 5, Text: " tail",
			Meta: map[string]string{"status": "running"},
			Runs: []Run{{Text: "art", Flags: 1, FG: "#ff0000"}}},
	}}
	got := encodeDecodeFrame(t, f)
	if len(got.Blocks) != 2 {
		t.Fatalf("decoded blocks = %+v", got.Blocks)
	}
	if got.Blocks[0].Op != "reset" {
		t.Errorf("decoded op 0 = %+v, want reset", got.Blocks[0])
	}
	b := got.Blocks[1]
	if b.Op != "set" || b.ID != 3 || b.Kind != "tool" || b.From != 5 || b.Text != " tail" ||
		b.Meta["status"] != "running" || len(b.Runs) != 1 || b.Runs[0].FG != "#ff0000" {
		t.Errorf("decoded block = %+v", b)
	}
}

// encodeDecodeFrame round-trips one frame through the wire codec.
func encodeDecodeFrame(t *testing.T, f *Frame) *Frame {
	t.Helper()
	payload, err := EncodePayload(f)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := NewFrameCodec().DecodeFrame(payload.Data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}
