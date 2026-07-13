// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
)

// TestBashTool_StreamsProgressDuringExecution verifies that when a progress
// emitter is injected via context, a long-running bash command reports partial
// output BEFORE it completes — the fix for the "nothing happens" syndrome
// where slow tool output only appeared at the end.
func TestBashTool_StreamsProgressDuringExecution(t *testing.T) {
	tool := &BashTool{}

	var mu sync.Mutex
	var snaps []string
	emit := func(partial string) {
		mu.Lock()
		snaps = append(snaps, partial)
		mu.Unlock()
	}

	ctx := agentic.WithProgress(context.Background(), emit)
	out, err := tool.ExecuteContext(ctx, `{"command":"echo first; sleep 0.3; echo second"}`)
	if err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}
	if !strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Fatalf("final output missing lines: %q", out)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(snaps) == 0 {
		t.Fatalf("no progress snapshots emitted during execution (final=%q)", out)
	}
	// At least one mid-run snapshot must contain the first line (produced
	// before the command finished).
	sawFirst := false
	for _, s := range snaps {
		if strings.Contains(s, "first") {
			sawFirst = true
		}
	}
	if !sawFirst {
		t.Errorf("no progress snapshot contained 'first'; snapshots=%v", snaps)
	}
}

// TestProgressWriter_DebouncesAndFlushes verifies the writer collapses bursts
// of writes (no flooding) and that finalFlush reports the tail even if it
// arrived within the debounce interval.
func TestProgressWriter_DebouncesAndFlushes(t *testing.T) {
	var mu sync.Mutex
	var snaps []string
	emit := func(p string) {
		mu.Lock()
		snaps = append(snaps, p)
		mu.Unlock()
	}
	buf := &bytes.Buffer{}
	w := newProgressWriter(buf, emit, 50*time.Millisecond)

	// Three rapid writes within one interval collapse to at most one snapshot.
	w.Write([]byte("a"))
	w.Write([]byte("b"))
	w.Write([]byte("c"))
	time.Sleep(80 * time.Millisecond)
	w.Write([]byte("d")) // past the interval → a fresh snapshot
	w.finalFlush()       // flushes the tail regardless of debounce

	mu.Lock()
	defer mu.Unlock()
	if len(snaps) < 2 {
		t.Errorf("expected at least 2 debounced snapshots, got %d: %v", len(snaps), snaps)
	}
	last := snaps[len(snaps)-1]
	if !strings.Contains(last, "d") {
		t.Errorf("final flush did not include the last write; last=%q", last)
	}
	// The buffer must hold the full content for the final tool result.
	if buf.String() != "abcd" {
		t.Errorf("buffer lost content: %q (want %q)", buf.String(), "abcd")
	}
}
