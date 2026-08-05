// SPDX-License-Identifier: GPL-3.0-or-later

package usage

import (
	"testing"
	"time"
)

// TestStore_Busts covers the E2 enhancement (ENHANCE.md): count provider
// cache busts recorded in the event stream. A bust is a turn whose cache_read
// collapsed to ~0 AFTER the same model's cache was established by an earlier
// turn (provider TTL expiry / prefix invalidation) — it converts cheap cached
// re-reads into expensive fresh input. Cold starts (cache never established)
// and cache-less providers (always 0) must NOT count.
func TestStore_Busts(t *testing.T) {
	st := openTemp(t)
	defer st.Close()

	add := func(model string, cacheRead int) {
		if err := st.Add(Record{Project: "/a", Provider: "p", Model: model, PromptN: 100, CacheRead: cacheRead}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	// glm-5.2: establish (100), hit (140), bust (0), re-warm (138), bust (0) => 2 busts.
	add("glm-5.2", 100)
	add("glm-5.2", 140)
	add("glm-5.2", 0)
	add("glm-5.2", 138)
	add("glm-5.2", 0)
	// local: cache never established (always 0) => 0 busts.
	add("local", 0)
	add("local", 0)
	// claude: establish (50), partial drop within tolerance (48) => 0 busts.
	add("claude", 50)
	add("claude", 48)

	busts, err := st.Busts("", time.Time{})
	if err != nil {
		t.Fatalf("Busts: %v", err)
	}
	if busts != 2 {
		t.Errorf("Busts = %d, want 2 (two zero-read collapses after establishment)", busts)
	}
}

// TestStore_BustsProjectFilter verifies the project scoping applies.
func TestStore_BustsProjectFilter(t *testing.T) {
	st := openTemp(t)
	defer st.Close()
	add := func(project string, cacheRead int) {
		if err := st.Add(Record{Project: project, Provider: "p", Model: "m", PromptN: 10, CacheRead: cacheRead}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	add("/a", 100)
	add("/a", 0) // bust in /a
	add("/b", 100)
	add("/b", 0) // bust in /b

	busts, err := st.Busts("/a", time.Time{})
	if err != nil {
		t.Fatalf("Busts: %v", err)
	}
	if busts != 1 {
		t.Errorf("Busts(/a) = %d, want 1 (project filter must scope the count)", busts)
	}
}
