// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package usage

import (
	"testing"
	"time"
)

// TestStore_Rows covers the raw per-completion time series used by
// /stats:cache: chronological order, project/since filters, and the
// most-recent-N limit.
func TestStore_Rows(t *testing.T) {
	st := openTemp(t)
	base := time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC)
	recs := []Record{
		{Project: "/a", Provider: "p", Model: "m1", PromptN: 100, CacheRead: 50, At: base},
		{Project: "/b", Provider: "p", Model: "m2", PromptN: 10, At: base.Add(time.Minute)},
		{Project: "/a", Provider: "p", Model: "m1", PromptN: 200, CacheRead: 150, At: base.Add(2 * time.Minute)},
		{Project: "/a", Provider: "p", Model: "m1", PromptN: 300, At: base.Add(3 * time.Minute)},
	}
	for _, r := range recs {
		if err := st.Add(r); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	t.Run("chronological with all fields", func(t *testing.T) {
		rows, err := st.Rows("", time.Time{}, 0)
		if err != nil {
			t.Fatalf("Rows: %v", err)
		}
		if len(rows) != 4 {
			t.Fatalf("rows = %d, want 4", len(rows))
		}
		for i := 1; i < len(rows); i++ {
			if rows[i].At.Before(rows[i-1].At) {
				t.Fatalf("rows not chronological at %d: %+v", i, rows)
			}
		}
		if rows[0].CacheRead != 50 || !rows[0].At.Equal(base) {
			t.Errorf("first row = %+v, want CacheRead=50 At=base", rows[0])
		}
	})

	t.Run("project filter", func(t *testing.T) {
		rows, err := st.Rows("/a", time.Time{}, 0)
		if err != nil {
			t.Fatalf("Rows: %v", err)
		}
		if len(rows) != 3 {
			t.Fatalf("rows = %d, want 3 for /a", len(rows))
		}
		for _, r := range rows {
			if r.Project != "/a" {
				t.Errorf("row project = %q, want /a", r.Project)
			}
		}
	})

	t.Run("since filter", func(t *testing.T) {
		rows, err := st.Rows("", base.Add(2*time.Minute), 0)
		if err != nil {
			t.Fatalf("Rows: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("rows = %d, want 2 since base+2m", len(rows))
		}
	})

	t.Run("limit keeps the most recent", func(t *testing.T) {
		rows, err := st.Rows("", time.Time{}, 2)
		if err != nil {
			t.Fatalf("Rows: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("rows = %d, want 2", len(rows))
		}
		// Newest two, still chronological: base+2m then base+3m.
		if !rows[0].At.Equal(base.Add(2*time.Minute)) || !rows[1].At.Equal(base.Add(3*time.Minute)) {
			t.Errorf("rows = %+v, want the two newest in order", rows)
		}
	})
}
