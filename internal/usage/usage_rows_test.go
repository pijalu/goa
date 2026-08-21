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
	addRecords(t, st, recs)

	t.Run("chronological with all fields", func(t *testing.T) {
		rows := rowsForTest(t, st, "", time.Time{}, 0)
		assertRows(t, rows, 4)
		assertChronological(t, rows)
		if rows[0].CacheRead != 50 || !rows[0].At.Equal(base) {
			t.Errorf("first row = %+v, want CacheRead=50 At=base", rows[0])
		}
	})
	t.Run("project filter", func(t *testing.T) {
		rows := rowsForTest(t, st, "/a", time.Time{}, 0)
		assertRows(t, rows, 3)
		assertProject(t, rows, "/a")
	})
	t.Run("since filter", func(t *testing.T) {
		rows := rowsForTest(t, st, "", base.Add(2*time.Minute), 0)
		if len(rows) != 2 {
			t.Fatalf("rows = %d, want 2 since base+2m", len(rows))
		}
	})
	t.Run("limit keeps the most recent", func(t *testing.T) {
		rows := rowsForTest(t, st, "", time.Time{}, 2)
		if len(rows) != 2 {
			t.Fatalf("rows = %d, want 2", len(rows))
		}
		if !rows[0].At.Equal(base.Add(2*time.Minute)) || !rows[1].At.Equal(base.Add(3*time.Minute)) {
			t.Errorf("rows = %+v, want the two newest in order", rows)
		}
	})
}

func assertProject(t *testing.T, rows []Record, want string) {
	t.Helper()
	for _, row := range rows {
		if row.Project != want {
			t.Errorf("row project = %q, want %s", row.Project, want)
		}
	}
}

func assertRows(t *testing.T, rows []Record, want int) {
	t.Helper()
	if len(rows) != want {
		t.Fatalf("rows = %d, want %d", len(rows), want)
	}
}

func addRecords(t *testing.T, st *Store, records []Record) {
	t.Helper()
	for _, record := range records {
		if err := st.Add(record); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
}

func rowsForTest(t *testing.T, st *Store, project string, since time.Time, limit int) []Record {
	t.Helper()
	rows, err := st.Rows(project, since, limit)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	return rows
}

func assertChronological(t *testing.T, rows []Record) {
	t.Helper()
	for i := 1; i < len(rows); i++ {
		if rows[i].At.Before(rows[i-1].At) {
			t.Fatalf("rows not chronological at %d: %+v", i, rows)
		}
	}
}
