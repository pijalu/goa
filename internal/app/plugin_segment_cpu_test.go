// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"runtime"
	"testing"

	"github.com/pijalu/goa/plugins"
)

// TestCPU_CarouselSteadyState asserts the carousel refresh path is cheap and
// doesn't leak goroutines. Heap is measured per-tick via allocation counting
// (robust under -race) rather than absolute HeapAlloc (flaky with a
// concurrent render loop + race instrumentation).
func TestCPU_CarouselSteadyState(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	rt := newPluginRuntime(sc.app.subs)
	n := 0
	rt.ui.AddSegment(plugins.UISegmentDef{
		ID: "quota", Priority: 10,
		Render: func() string { n++; return "5h:" + string(rune('0'+n%10)) + "%" },
	})
	sc.app.subs.setPluginRT(rt)
	sc.app.activatePluginUI(sc.engine)

	base := runtime.NumGoroutine()

	// Measure allocations per carousel tick. The refresh path must not churn
	// memory — it's on the steady-state (3s) loop.
	allocs := testing.AllocsPerRun(200, func() {
		sc.app.pushPluginSegments(sc.engine)
	})
	sc.engine.RenderNow()

	if grew := runtime.NumGoroutine() - base; grew > 4 {
		t.Fatalf("goroutines grew by %d over 200 ticks (leak); base=%d now=%d", grew, base, runtime.NumGoroutine())
	}
	// pushPluginSegments allocates a small slice + a few strings per segment.
	// Bound generously at 200 allocs/tick; correct behavior is a handful.
	if allocs > 200 {
		t.Fatalf("carousel tick allocated %.0f objects (want < 200)", allocs)
	}
	t.Logf("carousel tick: %.1f allocs, goroutines +%d", allocs, runtime.NumGoroutine()-base)
}

// BenchmarkPushPluginSegments measures one carousel refresh (re-evaluate
// segments + push to footer + RequestRender). Sub-microsecond expected.
func BenchmarkPushPluginSegments(b *testing.B) {
	sc := newUIScenario(b, 100, 24)
	rt := newPluginRuntime(sc.app.subs)
	rt.ui.AddSegment(plugins.UISegmentDef{ID: "quota", Priority: 10, Render: func() string { return "5h:42% / 5d:30%" }})
	sc.app.subs.setPluginRT(rt)
	sc.app.activatePluginUI(sc.engine)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sc.app.pushPluginSegments(sc.engine)
	}
}

// BenchmarkFooterRenderWithSegment measures footer Render with a segment
// present — the incremental per-frame cost the render loop pays.
func BenchmarkFooterRenderWithSegment(b *testing.B) {
	sc := newUIScenario(b, 100, 24)
	rt := newPluginRuntime(sc.app.subs)
	rt.ui.AddSegment(plugins.UISegmentDef{ID: "quota", Priority: 10, Render: func() string { return "5h:42% / 5d:30%" }})
	sc.app.subs.setPluginRT(rt)
	sc.app.activatePluginUI(sc.engine)
	sc.app.pushPluginSegments(sc.engine)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sc.footer.Render(100)
	}
}
