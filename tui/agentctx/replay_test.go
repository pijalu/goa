// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentctx

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/tui"
)

// recordingTerminal is a thread-safe tui.Terminal that captures every write.
// The ReplayRunner emits on its own goroutine while the test may read the
// captured bytes concurrently, so writes are mutex-guarded (unlike the
// single-goroutine testTerminal in internal/app).
type recordingTerminal struct {
	mu     sync.Mutex
	writes []string
	w, h   int
	// failAfter, when >= 0, makes Write return an error once the cumulative
	// number of writes exceeds it — used to exercise failure isolation.
	failAfter int
	err       error
	// gate, when non-nil, is received-from once per Write before recording, so
	// a test can pace/block emission and interleave a Cancel deterministically.
	// Accessed only under mu (set via setGate) to stay race-clean.
	gate <-chan struct{}
}

func newRecordingTerminal(w, h int) *recordingTerminal {
	return &recordingTerminal{w: w, h: h, failAfter: -1}
}

// setGate installs a per-write pacing channel (nil disables pacing). Safe to
// call from the test goroutine while the runner is emitting.
func (t *recordingTerminal) setGate(g <-chan struct{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gate = g
}

// setFailAfter arms/disarms write failure injection, race-safe.
func (t *recordingTerminal) setFailAfter(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failAfter = n
	t.err = nil
}

func (t *recordingTerminal) Start(onInput func(string), onResize func()) {}
func (t *recordingTerminal) Stop()                                       {}

func (t *recordingTerminal) Write(p []byte) (int, error) {
	t.mu.Lock()
	gate := t.gate
	t.mu.Unlock()
	if gate != nil {
		<-gate // pace emission; blocks until the test releases one write
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.failAfter >= 0 && len(t.writes) >= t.failAfter {
		if t.err == nil {
			t.err = errReplayWrite
		}
		return 0, t.err
	}
	t.writes = append(t.writes, string(p))
	return len(p), nil
}

func (t *recordingTerminal) WriteString(s string)    { _, _ = t.Write([]byte(s)) }
func (t *recordingTerminal) Size() (int, int)        { return t.w, t.h }
func (t *recordingTerminal) SetRaw() (func(), error) { return func() {}, nil }
func (t *recordingTerminal) HideCursor()             {}
func (t *recordingTerminal) ShowCursor()             {}
func (t *recordingTerminal) ClearScreen()            {}
func (t *recordingTerminal) SetTitle(string)         {}

// output returns the concatenation of all captured writes.
func (t *recordingTerminal) output() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.writes, "")
}

// writeCount returns the number of terminal writes so far.
func (t *recordingTerminal) writeCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.writes)
}

// errReplayWrite is the sentinel write failure used by recordingTerminal.
var errReplayWrite = &replayWriteError{}

type replayWriteError struct{}

func (e *replayWriteError) Error() string { return "terminal write failed" }

// makeCanvas builds a canvas of n rows with unique, position-identifying
// content so exact-emission and ordering assertions are unambiguous. Indices
// are zero-padded to a fixed width so no row's marker is a substring of
// another (e.g. row 1 must not match inside row 10).
func makeCanvas(n int) []string {
	rows := make([]string, n)
	for i := 0; i < n; i++ {
		rows[i] = "canvas-row-" + pad4(i)
	}
	return rows
}

// pad4 renders i as a zero-padded 4-digit string, making row markers
// prefix-free for substring counting.
func pad4(i int) string {
	var buf [4]byte
	for p := 3; p >= 0; p-- {
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[:])
}

// rowMarker returns the unique content of canvas row i.
func rowMarker(i int) string { return "canvas-row-" + pad4(i) }

// countOccurrences counts non-overlapping occurrences of needle in s.
func countOccurrences(s, needle string) int {
	return strings.Count(s, needle)
}

// awaitResult blocks until one ReplayResult arrives or the deadline passes.
func awaitResult(t *testing.T, r *ReplayRunner) ReplayResult {
	t.Helper()
	select {
	case res := <-r.Results():
		return res
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a ReplayResult")
		return ReplayResult{}
	}
}

// TestReplayRunner_EmitsExactlyOnce is the switch-fidelity core: a replay of
// rows [from, to) must emit each target row EXACTLY once, in order, with no
// duplicates and no omissions — the byte-level contract that makes a switched
// view's history scrollable without corruption.
func TestReplayRunner_EmitsExactlyOnce(t *testing.T) {
	term := newRecordingTerminal(80, 24)
	r := NewReplayRunner(tui.NewCompositor(term), 8) // small chunks force multiple writes
	defer r.Close()

	const total = 40
	canvas := makeCanvas(total)
	from, to := 5, 30

	r.Submit(ReplayRequest{AgentID: "agent-b", Canvas: canvas, From: from, To: to, Width: 80, Height: 24})
	res := awaitResult(t, r)

	if res.Err != nil {
		t.Fatalf("replay failed: %v", res.Err)
	}
	if res.AgentID != "agent-b" {
		t.Errorf("result AgentID = %q, want agent-b", res.AgentID)
	}
	if res.Emitted != to {
		t.Errorf("Emitted = %d, want %d (full range reached)", res.Emitted, to)
	}

	out := term.output()
	assertRangeEmittedExactlyOnce(t, out, total, from, to)
	assertRowsInOrder(t, out, from, to)
}

// assertRangeEmittedExactlyOnce asserts every row in [from, to) appears exactly
// once in out and every row outside that range never appears.
func assertRangeEmittedExactlyOnce(t *testing.T, out string, total, from, to int) {
	t.Helper()
	for i := 0; i < total; i++ {
		row := rowMarker(i)
		n := countOccurrences(out, row)
		inRange := i >= from && i < to
		if inRange && n != 1 {
			t.Errorf("row %d (%q) emitted %d times, want exactly 1", i, row, n)
		}
		if !inRange && n != 0 {
			t.Errorf("row %d (%q) outside [from,to) emitted %d times, want 0", i, row, n)
		}
	}
}

// assertRowsInOrder asserts row i precedes row i+1 across [from, to) in out.
func assertRowsInOrder(t *testing.T, out string, from, to int) {
	t.Helper()
	last := -1
	for i := from; i < to; i++ {
		idx := strings.Index(out, rowMarker(i))
		if idx < last {
			t.Errorf("row %d emitted out of order (idx %d after %d)", i, idx, last)
		}
		last = idx
	}
}

// TestReplayRunner_ZeroDuplicatesOnReturn is the return-switch fidelity
// contract: after a first switch replays a backlog and advances the saved
// watermark to Emitted, a SECOND switch to the same view with the advanced
// watermark (From = prior Emitted) emits ZERO rows — the committed history is
// never re-emitted.
func TestReplayRunner_ZeroDuplicatesOnReturn(t *testing.T) {
	term := newRecordingTerminal(80, 24)
	r := NewReplayRunner(tui.NewCompositor(term), 0)
	defer r.Close()

	const total = 60
	canvas := makeCanvas(total)

	// First switch to B: rows [10, 50) are committed-but-unemitted.
	r.Submit(ReplayRequest{AgentID: "b", Canvas: canvas, From: 10, To: 50, Width: 80, Height: 24})
	res1 := awaitResult(t, r)
	if res1.Err != nil || res1.Emitted != 50 {
		t.Fatalf("first replay: Emitted=%d Err=%v, want 50, nil", res1.Emitted, res1.Err)
	}
	firstBytes := term.output()
	if countOccurrences(firstBytes, rowMarker(20)) != 1 {
		t.Fatalf("first replay must emit row 20 exactly once")
	}

	// The command loop applies res1.Emitted as the saved watermark. On the
	// return switch the backlog range is empty (From >= To), so Submit drops
	// the request and no run executes (no result is produced).
	r.Submit(ReplayRequest{AgentID: "b", Canvas: canvas, From: res1.Emitted, To: 50, Width: 80, Height: 24})
	select {
	case res2 := <-r.Results():
		t.Fatalf("empty-range submit produced an unexpected result: %+v", res2)
	case <-time.After(150 * time.Millisecond):
		// expected: dropped, nothing emitted
	}

	// B grows while inactive to a new natural top; only the NEW rows are
	// emitted on the next switch, never the previously-committed ones.
	r.Submit(ReplayRequest{AgentID: "b", Canvas: canvas, From: res1.Emitted, To: total, Width: 80, Height: 24})
	res3 := awaitResult(t, r)
	if res3.Emitted != total {
		t.Fatalf("growth replay Emitted = %d, want %d", res3.Emitted, total)
	}
	final := term.output()
	// Rows [10, 50) were emitted once (first replay) and never again.
	for i := 10; i < 50; i++ {
		if n := countOccurrences(final, rowMarker(i)); n != 1 {
			t.Errorf("committed row %d emitted %d times total, want exactly 1 (no dup on return)", i, n)
		}
	}
	// Rows [50, 60) emitted exactly once by the growth replay.
	for i := 50; i < total; i++ {
		if n := countOccurrences(final, rowMarker(i)); n != 1 {
			t.Errorf("growth row %d emitted %d times, want exactly 1", i, n)
		}
	}
}

// TestReplayRunner_CancelCoalescesToLatest verifies the single-writer and
// cancel+coalesce contract: submitting a newer request while one is in flight
// cancels the in-flight run, replaces any pending request, and only the
// latest target runs to completion — never two concurrent replays, never a
// stale backlog.
func TestReplayRunner_CancelCoalescesToLatest(t *testing.T) {
	term := newRecordingTerminal(80, 24)
	// Pace emission so the first run (b) is verifiably IN FLIGHT when the
	// superseding submits land — otherwise a fast in-memory run completes
	// before the next Submit and there is nothing to coalesce. Unbuffered
	// gate: the run parks on the first chunk-write until we release it.
	gate := make(chan struct{})
	term.setGate(gate)
	// chunkRows 1 maximizes the number of inter-chunk cancellation checks, so
	// a supersede lands deterministically mid-flight.
	r := NewReplayRunner(tui.NewCompositor(term), 1)
	defer func() {
		term.setGate(nil)
		r.Close()
	}()

	const total = 200
	canvas := makeCanvas(total)

	// Submit b while gated, then release ONE chunk-write. The unbuffered
	// handoff confirms b's run has started (run() sets runCancel before the
	// first write), so the superseding submits below cancel a run that is
	// verifiably in flight and parked on its next chunk.
	r.Submit(ReplayRequest{AgentID: "b", Canvas: canvas, From: 0, To: total, Width: 80, Height: 24})
	gate <- struct{}{} // b's first chunk completes; b parks on the second
	r.Submit(ReplayRequest{AgentID: "c", Canvas: canvas, From: 0, To: total, Width: 80, Height: 24})
	r.Submit(ReplayRequest{AgentID: "d", Canvas: canvas, From: 0, To: total, Width: 80, Height: 24})
	// Open the gate: b observes its cancellation between chunks and d (the
	// latest) runs to completion.
	close(gate)
	term.setGate(nil)

	// Collect results until the single fully-completing run arrives (the
	// LATEST target, d) or the deadline passes. Earlier targets either never
	// execute (coalesced away) or report cancellation; exactly one run may
	// reach Emitted == total.
	results, completed := collectUntilComplete(t, r, total)

	if completed.AgentID != "d" {
		t.Errorf("completing run = %q, want the latest target d; results=%+v", completed.AgentID, results)
	}
	// No superseded target may have completed.
	for _, res := range results {
		if res.AgentID != "d" && res.Err == nil && res.Emitted == total {
			t.Errorf("superseded target %s completed; results=%+v", res.AgentID, results)
		}
	}
	// Serialization: the completing run's final row reached the terminal.
	if !strings.Contains(term.output(), rowMarker(total-1)) {
		t.Error("latest replay never emitted its final row")
	}
}

// collectUntilComplete drains the runner's results until one run completes
// (Err == nil and Emitted == total), returning all results seen plus the
// completing one. Fails the test if no run completes within the deadline.
func collectUntilComplete(t *testing.T, r *ReplayRunner, total int) (results []ReplayResult, completed *ReplayResult) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for completed == nil {
		select {
		case res := <-r.Results():
			results = append(results, res)
			if res.Err == nil && res.Emitted == total {
				c := res
				completed = &c
			}
		case <-deadline:
			t.Fatalf("no run completed; results=%+v", results)
		}
	}
	return results, completed
}

// TestReplayRunner_CancelStopsEmission verifies Cancel() halts an in-flight
// run without scheduling a new one: the result reports a partial watermark
// and a non-nil cause, and no further emission happens after. Emission is
// paced by a gate channel so the cancel lands deterministically mid-flight
// (an un-paced in-memory replay would finish before the cancel arrives).
func TestReplayRunner_CancelStopsEmission(t *testing.T) {
	term := newRecordingTerminal(80, 24)
	// Gate: each Write blocks until released, so the run stays in flight while
	// the test cancels. Unbuffered so a write is verifiably parked before the
	// cancel lands.
	gate := make(chan struct{})
	term.setGate(gate)
	r := NewReplayRunner(tui.NewCompositor(term), 1)
	defer func() {
		term.setGate(nil) // stop pacing new writes
		r.Close()
	}()

	const total = 500
	canvas := makeCanvas(total)
	r.Submit(ReplayRequest{AgentID: "b", Canvas: canvas, From: 0, To: total, Width: 80, Height: 24})

	// Release two chunk-writes (each handoff confirms the run is mid-flight).
	gate <- struct{}{}
	gate <- struct{}{}
	r.Cancel()
	close(gate) // releases any write currently parked on <-gate
	term.setGate(nil)
	res := awaitResult(t, r)

	if res.Err == nil {
		t.Error("cancelled run must report a non-nil Err (the cancellation cause)")
	}
	if res.Emitted > total {
		t.Errorf("cancelled Emitted = %d exceeds range end %d", res.Emitted, total)
	}
	// Failure isolation: the partial watermark is still a valid count of rows
	// physically present, so the caller can advance by exactly that.
	bytesAtCancel := term.writeCount()
	time.Sleep(50 * time.Millisecond)
	if got := term.writeCount(); got != bytesAtCancel {
		t.Errorf("emission continued after Cancel: writes %d -> %d", bytesAtCancel, got)
	}
}

// TestReplayRunner_FailureIsolated verifies a terminal write error is
// contained to the replay: the run reports the error and the partial
// watermark, the runner goroutine survives, and a subsequent replay still
// works (the main UI stays live).
func TestReplayRunner_FailureIsolated(t *testing.T) {
	term := newRecordingTerminal(80, 24)
	r := NewReplayRunner(tui.NewCompositor(term), 4)
	defer r.Close()

	const total = 100
	canvas := makeCanvas(total)

	// Fail after 2 writes (one partial chunk region).
	term.setFailAfter(2)
	r.Submit(ReplayRequest{AgentID: "b", Canvas: canvas, From: 0, To: total, Width: 80, Height: 24})
	res := awaitResult(t, r)
	if res.Err == nil {
		t.Error("run must report the write error")
	}
	if res.Emitted >= total {
		t.Errorf("failed run Emitted = %d, want a partial watermark < %d", res.Emitted, total)
	}

	// The runner is still alive: a follow-up replay (writes now succeeding)
	// completes and emits its rows.
	term.setFailAfter(-1)
	r.Submit(ReplayRequest{AgentID: "b", Canvas: canvas, From: res.Emitted, To: total, Width: 80, Height: 24})
	res2 := awaitResult(t, r)
	if res2.Err != nil || res2.Emitted != total {
		t.Errorf("runner did not survive the failure: Emitted=%d Err=%v", res2.Emitted, res2.Err)
	}
}

// TestReplayRunner_NeverMutatesLiveCompositorState guards the R1 ownership
// invariant: the runner must not touch the live compositor's prevLines /
// scrollTop / vt baseline. We snapshot ExportFrame before and after a replay
// and assert byte-identity — the runner only returns a watermark; applying it
// is the command loop's job.
func TestReplayRunner_NeverMutatesLiveCompositorState(t *testing.T) {
	term := newRecordingTerminal(80, 24)
	comp := tui.NewCompositor(term)
	r := NewReplayRunner(comp, 0)
	defer r.Close()

	before := comp.ExportFrame()
	canvas := makeCanvas(50)
	r.Submit(ReplayRequest{AgentID: "b", Canvas: canvas, From: 0, To: 40, Width: 80, Height: 24})
	res := awaitResult(t, r)
	if res.Err != nil {
		t.Fatalf("replay failed: %v", res.Err)
	}
	after := comp.ExportFrame()

	if before.ScrollTop != after.ScrollTop {
		t.Errorf("runner mutated live ScrollTop: %d -> %d", before.ScrollTop, after.ScrollTop)
	}
	if before.VT != after.VT {
		t.Errorf("runner mutated live VT: %d -> %d", before.VT, after.VT)
	}
	if len(before.PrevLines) != len(after.PrevLines) {
		t.Errorf("runner mutated live PrevLines length: %d -> %d", len(before.PrevLines), len(after.PrevLines))
	}
	for i := range before.PrevLines {
		if i < len(after.PrevLines) && before.PrevLines[i] != after.PrevLines[i] {
			t.Errorf("runner mutated live PrevLines[%d]", i)
		}
	}
	// The watermark only travels back via the result message.
	if res.Emitted != 40 {
		t.Errorf("returned watermark = %d, want 40 (handed back, not applied)", res.Emitted)
	}
}

// TestReplayRunner_ConcurrentSubmitClose exercises the runner under -race:
// many submits from multiple goroutines plus a Close must not race and must
// not deadlock. The final state is a closed runner.
func TestReplayRunner_ConcurrentSubmitClose(t *testing.T) {
	term := newRecordingTerminal(80, 24)
	r := NewReplayRunner(tui.NewCompositor(term), 1)

	canvas := makeCanvas(120)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				r.Submit(ReplayRequest{AgentID: id, Canvas: canvas, From: 0, To: 120, Width: 80, Height: 24})
			}
		}(string(rune('a' + g)))
	}
	wg.Wait()
	r.Cancel()
	r.Close()
	// After Close the runner goroutine is gone; draining results must not block.
	select {
	case <-r.Results():
	default:
	}
}
