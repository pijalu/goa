// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The B16 surface: the /quota table's Status cell must name the moment an
// over-budget window runs out ("over budget — exhausted at 14:32").
//
// These drive the plugin's real renderer with a seeded cache: the cache is what
// /quota prints from, and seeding it directly keeps the test on the projection
// and the wording instead of on a provider fixture's timestamp conventions.

// frozenNow is the instant every test in this file pins the plugin's clock to.
var frozenNow = time.Date(2026, 10, 5, 9, 0, 0, 0, time.Local)

// ms converts a duration to the milliseconds the plugin's limit fields are in.
// Spelling these as int64(4*time.Hour) would pass nanoseconds — a 1000x window
// that still yields the same *ratio*, so the projection looks right while the
// countdown reads +41 days.
func ms(d time.Duration) int64 { return d.Milliseconds() }

// freezeClock pins the plugin's view of "now" (Date.now, which is what the pace
// projection reads) so the projected run-out instant is a fixed number rather
// than a race with the test runner.
func freezeClock(t *testing.T, env *quotaTestEnv, nowMs int64) {
	t.Helper()
	if env.bridge == nil {
		t.Fatal("freezeClock called before env.load")
	}
	env.evalJS(t, fmt.Sprintf("Date.now = function() { return %d; };", nowMs))
}

// clockOf renders an epoch-ms instant the way the plugin's format.clock does:
// the local wall clock, HH:MM.
func clockOf(ms int64) string {
	return time.UnixMilli(ms).Format("15:04")
}

// seedWindow puts one provider snapshot with a single window in the cache and
// renders the full /quota table from it.
func seedWindow(t *testing.T, env *quotaTestEnv, provider string, used, limit int, resetsAtMs, periodMs int64) string {
	t.Helper()
	env.evalJS(t, fmt.Sprintf(`
		_cache[%q] = {
			plan: "pro",
			limits: [{label: "Session (5h)", used: %d, limit: %d, resetsAt: %d, periodMs: %d}],
			_fetchedAt: Date.now()
		};`, provider, used, limit, resetsAtMs, periodMs))
	return env.evalJSValue(t, `renderFull(false)`)
}

// windowRow returns the rendered table line for the seeded window.
func windowRow(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Session (5h)") {
			return line
		}
	}
	t.Fatalf("no window row in the rendered table:\n%s", out)
	return ""
}

// assertFrozenClock checks that the plugin's clock really is the instant the
// test pinned, so a failure downstream is about the projection and never about a
// clock the harness left in another state.
func assertFrozenClock(t *testing.T, env *quotaTestEnv, wantMs int64) {
	t.Helper()
	if got := env.evalJSValue(t, `String(Date.now())`); got != fmt.Sprintf("%d", wantMs) {
		t.Fatalf("the plugin clock is %s, want %d: the test must pin Date.now before rendering", got, wantMs)
	}
}

// TestQuotaRunOut_OverBudgetNamesTheTime is B16's core case: 90% used 4 h into a
// 5 h window runs out at t* = elapsed*limit/used = 4 h 26 m 40 s into the window,
// i.e. 26 m 40 s from now — and the row must say so, in local time.
func TestQuotaRunOut_OverBudgetNamesTheTime(t *testing.T) {
	elapsedMs := ms(4 * time.Hour)
	periodMs := ms(5 * time.Hour)
	nowMs := frozenNow.UnixMilli()

	env := newQuotaTestEnv(t)
	env.load(t)
	freezeClock(t, env, nowMs)
	assertFrozenClock(t, env, nowMs)
	out := seedWindow(t, env, "zai", 90, 100, nowMs+(periodMs-elapsedMs), periodMs)

	runOut := nowMs - elapsedMs + elapsedMs*100/90 // windowStart + elapsed*limit/used
	want := "over budget — exhausted at " + clockOf(runOut)
	row := windowRow(t, out)
	if !strings.Contains(row, want) {
		t.Errorf("window row = %q, want it to contain %q", row, want)
	}
	// The window resets 1 h from now: if the row does not say that, the fixture
	// is not the 4h-into-5h window this test claims to be.
	if !strings.Contains(row, "+1h") {
		t.Errorf("window row = %q, want the 1 h countdown to the reset", row)
	}
}

// TestQuotaRunOut_AtResetMatchesTheSameProjection pins that the "At reset"
// column and the run-out instant are one projection, so the row cannot say
// "112% at reset" while naming a time that contradicts it.
func TestQuotaRunOut_AtResetMatchesTheSameProjection(t *testing.T) {
	elapsedMs := ms(4 * time.Hour)
	periodMs := ms(5 * time.Hour)
	nowMs := frozenNow.UnixMilli()

	env := newQuotaTestEnv(t)
	env.load(t)
	freezeClock(t, env, nowMs)
	assertFrozenClock(t, env, nowMs)
	out := seedWindow(t, env, "zai", 90, 100, nowMs+(periodMs-elapsedMs), periodMs)

	// 0.9 / (4/5) = 1.125 → 113% projected at reset (the At reset column,
	// rounded from .5 up).
	row := windowRow(t, out)
	if !strings.Contains(row, "113%") {
		t.Errorf("window row = %q, want the projected 113%% at reset (clock=%s)", row, env.evalJSValue(t, `String(Date.now())`))
	}
	runOut := nowMs - elapsedMs + elapsedMs*100/90
	if !strings.Contains(row, "exhausted at "+clockOf(runOut)) {
		t.Errorf("window row = %q, want the run-out time %s", row, clockOf(runOut))
	}
}

// TestQuotaRunOut_NotOverBudgetStaysInWords: a window inside its budget keeps the
// old wording, and no clock appears where nothing is going to run out.
func TestQuotaRunOut_NotOverBudgetStaysInWords(t *testing.T) {
	elapsedMs := ms(4 * time.Hour)
	periodMs := ms(5 * time.Hour)
	nowMs := frozenNow.UnixMilli()

	env := newQuotaTestEnv(t)
	env.load(t)
	freezeClock(t, env, nowMs)
	assertFrozenClock(t, env, nowMs)
	out := seedWindow(t, env, "zai", 40, 100, nowMs+(periodMs-elapsedMs), periodMs)

	row := windowRow(t, out)
	if strings.Contains(row, "exhausted at") {
		t.Errorf("a window inside its budget named a run-out time: %q", row)
	}
	if !strings.Contains(row, "plenty of room") {
		t.Errorf("window row = %q, want the in-budget wording", row)
	}
}

// TestQuotaRunOut_CloseToLimitStaysInWords keeps the middle band unchanged.
func TestQuotaRunOut_CloseToLimitStaysInWords(t *testing.T) {
	elapsedMs := ms(4 * time.Hour)
	periodMs := ms(5 * time.Hour)
	nowMs := frozenNow.UnixMilli()

	env := newQuotaTestEnv(t)
	env.load(t)
	freezeClock(t, env, nowMs)
	// 0.7 / (4/5) = 0.875 → warns, does not run out.
	out := seedWindow(t, env, "zai", 70, 100, nowMs+(periodMs-elapsedMs), periodMs)

	row := windowRow(t, out)
	if strings.Contains(row, "exhausted at") {
		t.Errorf("a window that fits its budget named a run-out time: %q", row)
	}
	if !strings.Contains(row, "close to limit") {
		t.Errorf("window row = %q, want the warn wording", row)
	}
}

// TestQuotaRunOut_WithoutTimingInfoKeepsTheWords: an over-budget window with no
// timing to project from reports "over budget" alone rather than inventing a
// clock time.
func TestQuotaRunOut_WithoutTimingInfoKeepsTheWords(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.load(t)
	freezeClock(t, env, frozenNow.UnixMilli())
	assertFrozenClock(t, env, frozenNow.UnixMilli())
	out := seedWindow(t, env, "zai", 120, 100, 0, 0)

	row := windowRow(t, out)
	if strings.Contains(row, "exhausted at") {
		t.Errorf("a window with no timing info named a run-out time: %q", row)
	}
	if !strings.Contains(row, "over budget") {
		t.Errorf("window row = %q, want the over-budget wording", row)
	}
}

// TestQuotaRunOut_JSONCarriesTheInstant keeps the machine-readable surface in
// step with the sentence: runOutAtMs is the single projection, and
// /quota:json's per-limit field carries it (null when there is nothing to
// project).
func TestQuotaRunOut_JSONCarriesTheInstant(t *testing.T) {
	const nowMs = int64(1_800_000_000_000)
	env := newQuotaTestEnv(t)
	env.load(t)
	freezeClock(t, env, nowMs)

	// null without a bounded window.
	env.evalJS(t, `var _plain = runOutAtMs({label: "cost", used: 500, limit: 0});`)
	if got := env.evalJSBool(t, `_plain === null`); !got {
		t.Error("runOutAtMs invented an instant for an unbounded window")
	}

	// 50 of 100 used, 1 h into a 2 h window → projected 100 → fits exactly.
	env.evalJS(t, `var _fits = runOutAtMs({used: 50, limit: 100, resetsAt: 1800003600000, periodMs: 7200000});`)
	if got := env.evalJSBool(t, `_fits === null`); !got {
		t.Error("runOutAtMs reported a run-out for a window that ends exactly at its limit")
	}

	// 75 of 100 used, 1 h into a 2 h window → projected 150 → the pace exhausts
	// the budget 1 h 20 m into the window, i.e. 20 m from now.
	env.evalJS(t, `var _over = runOutAtMs({used: 75, limit: 100, resetsAt: 1800003600000, periodMs: 7200000});`)
	wantMs := nowMs + 20*60*1000
	if got := env.evalJSBool(t, fmt.Sprintf(`_over === %d`, wantMs)); !got {
		t.Errorf("runOutAtMs(over) = %s, want %d", env.evalJSValue(t, `String(_over)`), wantMs)
	}

	// The JSON surface carries it per limit, next to the limit's own fields.
	env.evalJS(t, `var _json = JSON.parse(JSON.stringify(limitsWithRunOut([{label: "Session (5h)", used: 75, limit: 100, resetsAt: 1800003600000, periodMs: 7200000}])));`)
	if got := env.evalJSBool(t, fmt.Sprintf(`_json[0].exhaustedAt === %d`, wantMs)); !got {
		t.Errorf("limitsWithRunOut did not carry exhaustedAt: %s", env.evalJSValue(t, `JSON.stringify(_json)`))
	}
	if got := env.evalJSBool(t, `_json[0].label === "Session (5h)" && _json[0].used === 75 && _json[0].limit === 100`); !got {
		t.Error("limitsWithRunOut dropped the limit's own fields")
	}
}

// TestQuotaRunOut_ClockIsLocalHHMM pins the formatter itself: two digits, 24 h,
// local wall clock.
func TestQuotaRunOut_ClockIsLocalHHMM(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.load(t)
	for _, tc := range []struct {
		name string
		when time.Time
	}{
		{"morning", time.Date(2026, 10, 5, 9, 5, 0, 0, time.Local)},
		{"midnight", time.Date(2026, 10, 5, 0, 0, 0, 0, time.Local)},
		{"evening", time.Date(2026, 10, 5, 23, 59, 0, 0, time.Local)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := env.evalJSValue(t, fmt.Sprintf(`format.clock(%d)`, tc.when.UnixMilli()))
			if want := tc.when.Format("15:04"); got != want {
				t.Errorf("format.clock = %q, want %q", got, want)
			}
		})
	}
}
