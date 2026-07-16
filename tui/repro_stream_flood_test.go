// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestStreamFlood_BodyStillStreamsContent proves the body memoization does
// NOT freeze streaming: as write content grows across deltas the body updates
// (cache invalidates on each SetArgsPartial), and once args complete the full
// content is visible.
func TestStreamFlood_BodyStillStreamsContent(t *testing.T) {
	content := strings.Repeat("package main\n\nfunc f() int { return 1 }\n", 30)
	tc := NewToolExecution("write", "write main.go")

	// Stream growing content; the body must reflect the latest each step.
	prev := -1
	for i := 0; i <= len(content); i += 40 {
		tc.SetArgsPartial(fmt.Sprintf(`{"path":"main.go","content":"%s`, content[:i]))
		lines := strings.Join(tc.Render(80), "\n")
		// The number of visible content lines must grow (never go stale/frozen).
		got := strings.Count(lines, "func f()")
		if got < prev {
			t.Fatalf("body went backwards at i=%d: got %d < prev %d (streaming frozen?)", i, got, prev)
		}
		prev = got
	}
}

// BenchmarkStreamFlood_PatchTickNoChange simulates what patchRunningToolWidgets
// does every spinner frame: rebuild the box for a Running widget whose content
// has NOT changed. Before memoization this re-split/re-highlighted the whole
// body every tick; now it is a cache hit. This is the path that starved the
// command loop and froze the TUI when a large-content tool stayed Running.
func BenchmarkStreamFlood_PatchTickNoChange(b *testing.B) {
	content := strings.Repeat("package main\n\nfunc f() int { return 1 }\n", 150) // ~6 KB
	tc := NewToolExecution("write", "write main.go")
	tc.SetArgsJSON(fmt.Sprintf(`{"path":"big.go","content":%q}`, content))
	tc.SetStatus(ToolRunning)
	tc.Render(80) // prime the cache

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tc.updateBox() // spinner-tick style: no input change
	}
}

// BenchmarkStreamFlood_ContentGrowing measures the streaming path itself
// (content changes each delta → cache miss → recompute). Should be unchanged
// by memoization (it must recompute to show new content).
func BenchmarkStreamFlood_ContentGrowing(b *testing.B) {
	content := strings.Repeat("package main\n\nfunc f() int { return 1 }\n", 150)
	snaps := make([]string, 200)
	for i := range snaps {
		n := (i + 1) * len(content) / len(snaps)
		if n > len(content) {
			n = len(content)
		}
		snaps[i] = fmt.Sprintf(`{"path":"big.go","content":"%s`, content[:n])
	}
	tc := NewToolExecution("write", "write main.go")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tc.SetArgsPartial(snaps[i%len(snaps)])
	}
}
