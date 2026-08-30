// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/tui"
)

// TestCeilingPercentItemsPreserveAscendingOrder is the bugs.md regression:
// the /config percentage pickers (soft/trigger/hard ceilings) sorted
// alphabetically by label, interleaving 5% between 45% and 50% (and 100%
// between 10% and 15%). The ladder is an inherently ordered list, so every
// item must opt out of the label sort via PreserveOrder and the built order
// must read in ascending numeric order.
func TestCeilingPercentItemsPreserveAscendingOrder(t *testing.T) {
	items := ceilingPercentItems(false)
	if len(items) == 0 {
		t.Fatal("ceiling picker must not be empty")
	}
	for i, it := range items {
		if !it.PreserveOrder {
			t.Fatalf("item[%d] (%q) missing PreserveOrder: the alphabetical label sort interleaves 5%% after 45%% and 100%% after 10%%", i, it.Label)
		}
	}
	want := []string{"0% (disabled)"}
	for pct := 5; pct <= 100; pct += 5 {
		want = append(want, fmt.Sprintf("%d%%", pct))
	}
	if len(items) != len(want) {
		t.Fatalf("built %d items, want %d", len(items), len(want))
	}
	for i, w := range want {
		if items[i].Label != w {
			t.Errorf("built item[%d].Label = %q, want %q (ascending ladder)", i, items[i].Label, w)
		}
	}
}

// TestCeilingPercentPickerRendersAscending is the filmstrip check (guideline
// #5): the /config hard-limit picker rendered through the REAL selector with
// 5% active must show the ascending sequence the user actually reads —
// "0% (disabled), 5%, 10%, ..." — not 5% buried between 45% and 50%.
func TestCeilingPercentPickerRendersAscending(t *testing.T) {
	items := ceilingPercentItems(false)
	result := make(chan string, 1)
	s := tui.NewSelector("Hard ceiling (% of max tokens, 0 = disabled):", items, "5", result)

	lines := s.Render(80)
	plain := make([]string, len(lines))
	for i, l := range lines {
		plain[i] = ansi.Strip(l)
	}
	// With 5% active the window is anchored at the top of the list, so the
	// rendered rows are the exact bug scenario.
	want := []string{"0% (disabled)", "5%", "10%", "15%", "20%", "25%", "30%", "35%"}
	rendered := make([]string, 0, len(want))
	for _, l := range plain {
		f := strings.TrimSpace(l)
		if f == "" || strings.HasPrefix(f, "(") || strings.HasPrefix(f, "─") ||
			strings.HasPrefix(f, "Hard ceiling") || strings.Contains(f, "search>") ||
			strings.Contains(f, "nav") {
			continue
		}
		rendered = append(rendered, f)
	}
	if len(rendered) < len(want) {
		t.Fatalf("rendered %d item rows, want >= %d: %q", len(rendered), len(want), rendered)
	}
	for i, w := range want {
		if !strings.Contains(rendered[i], w) {
			t.Errorf("rendered row[%d] = %q, want it to show %q (rows: %q)", i, rendered[i], w, rendered)
		}
	}
}

// TestCeilingPercentItemsValuesUnpadded pins the value side: ordering is a
// display concern only — the persisted config values stay as-is ("5") so
// applySet keys and the ✓ current-value marker are unaffected. The per-model
// variant (inherit row prepended) shares the same ladder.
func TestCeilingPercentItemsValuesUnpadded(t *testing.T) {
	items := ceilingPercentItems(false)
	byValue := map[string]string{}
	for _, it := range items {
		byValue[it.Value] = it.Label
	}
	if got := byValue["5"]; got != "5%" {
		t.Errorf(`value "5" label = %q, want "5%%"`, got)
	}
	if got := byValue["100"]; got != "100%" {
		t.Errorf(`value "100" label = %q, want "100%%"`, got)
	}
	for _, it := range items {
		if it.Value == "0" {
			continue // the disabled row spells itself out
		}
		if _, err := strconv.Atoi(it.Value); err != nil {
			t.Fatalf("non-numeric value %q", it.Value)
		}
	}
	inh := percentItemsWithInherit()
	if inh[0].Label != "inherit (clear)" {
		t.Errorf("first per-model item = %q, want the inherit row", inh[0].Label)
	}
	if len(inh) != len(items)+1 {
		t.Errorf("per-model items = %d, want %d (inherit + ceiling rows)", len(inh), len(items)+1)
	}
}
