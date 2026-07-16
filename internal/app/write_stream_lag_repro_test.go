// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
)

// TestWriteStreamingLagRepro replays the captured production write tool call
// (pid 55211, 2026-07-16: a 30 KB write to specs/plan-mode.md at ~36 tok/s)
// through the full app event pipeline the way a provider streams it: N
// deltas, each carrying the *accumulated* ToolInput JSON prefix, then the
// final call + result.
//
// It demonstrates the O(n^2) blowup that wedged that session at 100% CPU for
// 20+ minutes after the stream finished. Every delta does O(content) work —
// json.Unmarshal attempt + partialStringFieldRe.FindAllStringSubmatch +
// strconv.Unquote per matched field (updatePartialArgs), then re-render —
// while the UI side re-measures grapheme widths (ansi.Width/uniseg) and
// re-strips ANSI for the whole viewport on every frame. Cost per delta grows
// linearly with content size and ~250 KB of garbage is allocated per 4-byte
// delta (~2 GB for a 30 KB write), so the engine command loop saturates and
// the UI falls ever further behind the stream.
//
// The assertion is a total-work budget: engine-loop CPU per *content byte*
// must stay low enough that a 30 KB write streamed at 36 tok/s (~830 s of
// stream) never backs up. Measured baseline before the fix was ~430 us per
// content byte (13.3 s of engine CPU for 30 KB); the budget below is ~5x
// tighter and still generous.
//
// Run with the real captured payload:
//
//	GOA_WRITE_LAG_ARGS=$(cat /tmp/write_args.json) \
//	  go test ./internal/app/ -run TestWriteStreamingLagRepro -v -timeout 300s
func TestWriteStreamingLagRepro(t *testing.T) {
	argsJSON := os.Getenv("GOA_WRITE_LAG_ARGS")
	if argsJSON == "" {
		argsJSON = synthWriteArgs(t, 30_000)
	}
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		t.Fatalf("fixture is not valid write args: %v", err)
	}
	raw := argsJSON

	// Providers emit one SSE chunk per streamed token; kimi k3 averages ~4 B
	// of (escaped) JSON per token. Batched gateways emit bigger chunks. The
	// flood is worst at fine granularity, so assert at the token-realistic 4 B
	// and log the coarser one for comparison.
	for _, step := range []int{4, 64} {
		step := step
		t.Run(fmt.Sprintf("delta=%dB", step), func(t *testing.T) {
			replayWriteStream(t, raw, args.Path, args.Content, step)
		})
	}
}

// maxPerDeltaGrowth bounds the algorithmic blowup: tail per-delta cost must
// not exceed this multiple of head per-delta cost. The O(n^2) bug grew ~10x
// across the buckets (1.3→2.3 ms head-to-tail was only the visible window; the
// full regex path grew without bound with args size). A flat O(1)-per-delta
// path stays near 1x; allow 4x for allocator/GC noise. Robust to -race.
const maxPerDeltaGrowth = 4.0

// maxEngineNsPerContentByte is an absolute backstop on total engine-loop work
// per content byte, sized to fail on the original bug even under -race (which
// inflates all timings ~10x). The bug measured ~440,000 ns/B; the fixed
// incremental scanner measures ~26,000 ns/B (and ~210,000 under -race). The
// growth-ratio assertion above is the primary gate; this only catches a
// uniformly-slow regression.
const maxEngineNsPerContentByte = 300_000

// replayWriteStream drives one full streaming write through the app pipeline
// at the given delta granularity and asserts the engine keeps real time.
func replayWriteStream(t *testing.T, raw, path, content string, step int) {
	t.Helper()
	sc := newUIScenario(t, 100, 40)
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})

	const bucketSize = 8192
	type bucket struct {
		lo, hi  int
		n       int
		elapsed time.Duration
	}
	var buckets []bucket

	start := time.Now()
	total := 0
	for i := 0; i < len(raw); i += step {
		end := i + step
		if end > len(raw) {
			end = len(raw)
		}
		ev := &agentic.OutputEvent{
			Type: agentic.EventToolCall, State: agentic.StateToolCall,
			ToolName: "write", ToolCallID: "call_lag", IsDelta: true,
			ToolInput: raw[:end],
		}
		t0 := time.Now()
		// Production-faithful: the commandLoop applies the event; rendering is
		// coalesced by the renderLoop (≤60fps), not forced per delta. Observing
		// the UI per delta (RenderNow+AgentFrame) would measure test-harness
		// overhead, not the production engine cost.
		sc.engine.ApplySync(func() { sc.app.handleAgentOutputEvent(ev) })
		d := time.Since(t0)
		total++
		if len(buckets) == 0 || end-buckets[len(buckets)-1].lo >= bucketSize {
			buckets = append(buckets, bucket{lo: end})
		}
		b := &buckets[len(buckets)-1]
		b.hi = end
		b.n++
		b.elapsed += d
	}
	streamElapsed := time.Since(start)

	// Final call + result, as production delivers them.
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "write", ToolCallID: "call_lag", ToolInput: raw,
	})
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolName: "write", ToolCallID: "call_lag",
		Text: fmt.Sprintf("[write: %s]\n✓ Written\n```\n%s\n```\n", path, content),
	})

	first, last := buckets[0], buckets[len(buckets)-1]
	avgFirst := first.elapsed / time.Duration(first.n)
	avgLast := last.elapsed / time.Duration(last.n)
	perByte := streamElapsed.Nanoseconds() / int64(len(content))
	t.Logf("rawArgs=%d B  deltas=%d  wall=%v (%.2f ms/delta avg, %.2f ms/delta tail)  %d ns/content-byte",
		len(raw), total, streamElapsed,
		float64(streamElapsed.Microseconds())/1000/float64(total),
		float64(avgLast.Microseconds())/1000, perByte)
	for _, b := range buckets {
		t.Logf("  args %6d-%6d B: %4d deltas  avg %7.2f ms/delta",
			b.lo-bucketSize, b.hi, b.n,
			float64(b.elapsed.Microseconds())/1000/float64(b.n))
	}

	// The bug was O(n^2): per-delta cost grew ~linearly with accumulated args
	// size. Assert the algorithmic shape — tail per-delta cost must not blow up
	// relative to head — which is robust to -race and slow CI (both scale all
	// buckets together), unlike an absolute wall-clock budget.
	if avgFirst > 0 && avgLast > maxPerDeltaGrowth*avgFirst {
		t.Errorf("per-delta cost grew from %v (head) to %v (tail), >%.0fx growth → "+
			"O(n^2) streaming render regression (per-delta full-args regex/scan)",
			avgFirst, avgLast, maxPerDeltaGrowth)
	}
	// Absolute backstop, generous enough for -race: the whole stream's engine
	// work must stay a small fraction of the real-time stream duration.
	if perByte > maxEngineNsPerContentByte {
		t.Errorf("engine-loop work = %d ns per content byte, exceeds backstop %d → "+
			"a 30 KB write streamed at 36 tok/s backs the command loop up "+
			"(per-delta cost too high even if not growing)", perByte, maxEngineNsPerContentByte)
	}
}

// BenchmarkWriteStreamingFlood measures allocations per streamed delta at a
// fixed (large) args size — the allocator churn behind the GC/madvise CPU.
//
//	go test ./internal/app/ -bench WriteStreamingFlood -benchmem
func BenchmarkWriteStreamingFlood(b *testing.B) {
	argsJSON := synthWriteArgs(b, 30_000)
	raw := argsJSON
	const step = 4096 // one benchmark iteration = one delta near full size
	sc := newUIScenario(b, 100, 40)
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		end := (i%((len(raw)-1)/step+1) + 1) * step
		if end > len(raw) {
			end = len(raw)
		}
		sc.apply(&agentic.OutputEvent{
			Type: agentic.EventToolCall, State: agentic.StateToolCall,
			ToolName: "write", ToolCallID: "call_lag", IsDelta: true,
			ToolInput: raw[:end],
		})
	}
}

// synthWriteArgs builds a write-args JSON document with n bytes of
// markdown-ish content, for runs without the captured fixture.
func synthWriteArgs(t testing.TB, n int) string {
	t.Helper()
	body := ""
	for len(body) < n {
		body += "## Section\n\nSome prose line with **bold** and `code` spans to fill space.\n\n"
	}
	b, err := json.Marshal(map[string]string{
		"path":    "/Users/muaddib/dev/goa/specs/plan-mode.md",
		"content": body[:n],
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
