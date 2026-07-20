// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package usage

import (
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestStore_AddAndQueryByProvider(t *testing.T) {
	st := openTemp(t)
	now := time.Now()
	recs := []Record{
		{Project: "/a", Provider: "kimi", Model: "k2", PromptN: 100, PredictedN: 50, At: now},
		{Project: "/a", Provider: "kimi", Model: "k2", PromptN: 60, PredictedN: 40, At: now},
		{Project: "/b", Provider: "zai", Model: "glm", PromptN: 10, PredictedN: 5, At: now},
	}
	for _, r := range recs {
		if err := st.Add(r); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	byProv, err := st.Query(ByProvider, "", time.Time{})
	if err != nil {
		t.Fatalf("Query ByProvider: %v", err)
	}
	if len(byProv) != 2 {
		t.Fatalf("ByProvider rows = %d, want 2", len(byProv))
	}
	// kimi has highest total → first (DESC).
	if byProv[0].Key != "kimi" {
		t.Errorf("top provider = %q, want kimi", byProv[0].Key)
	}
	if byProv[0].PromptN != 160 || byProv[0].PredictedN != 90 || byProv[0].Turns != 2 {
		t.Errorf("kimi stat = %+v, want prompt=160 predicted=90 turns=2", byProv[0])
	}
}

func TestStore_QueryFiltersByProject(t *testing.T) {
	st := openTemp(t)
	st.Add(Record{Project: "/a", Provider: "kimi", Model: "k2", PromptN: 100, PredictedN: 50})
	st.Add(Record{Project: "/b", Provider: "zai", Model: "glm", PromptN: 10, PredictedN: 5})

	// Per-project: /b only.
	got, err := st.Query(ByModel, "/b", time.Time{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 || got[0].Key != "glm" {
		t.Fatalf("per-project ByModel = %+v, want only glm", got)
	}

	// Global: both models.
	all, err := st.Query(ByModel, "", time.Time{})
	if err != nil {
		t.Fatalf("Query global: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("global ByModel rows = %d, want 2", len(all))
	}
}

func TestStore_Sum(t *testing.T) {
	st := openTemp(t)
	st.Add(Record{Project: "/a", Provider: "kimi", Model: "k2", PromptN: 100, PredictedN: 50, CacheRead: 7, CacheWrite: 3})
	st.Add(Record{Project: "/a", Provider: "kimi", Model: "k2", PromptN: 20, PredictedN: 10})

	sum, err := st.Sum("", time.Time{})
	if err != nil {
		t.Fatalf("Sum: %v", err)
	}
	if sum.Total() != 180 {
		t.Errorf("Sum total = %d, want 180", sum.Total())
	}
	if sum.CacheRead != 7 || sum.CacheWrite != 3 {
		t.Errorf("Sum cache = %+v, want read=7 write=3", sum)
	}
	if sum.Turns != 2 {
		t.Errorf("Sum turns = %d, want 2", sum.Turns)
	}
}

func TestStore_EmptyQueryReturnsNoRows(t *testing.T) {
	st := openTemp(t)
	got, err := st.Query(ByProject, "", time.Time{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty store rows = %d, want 0", len(got))
	}
}

func TestOpen_CreatesDirAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nested", "sub", "usage.db")
	s1, err := Open(p)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s1.Close()
	// Re-open same path (schema CREATE IF NOT EXISTS must not error).
	s2, err := Open(p)
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	s2.Close()
}

func TestStore_QuerySinceFiltersOldEvents(t *testing.T) {
	st := openTemp(t)
	now := time.Now()
	st.Add(Record{Project: "/a", Provider: "kimi", Model: "k2", PromptN: 100, At: now.AddDate(0, 0, -40)})
	st.Add(Record{Project: "/a", Provider: "kimi", Model: "k2", PromptN: 50, At: now.AddDate(0, 0, -2)})

	rows, err := st.Query(ByModel, "", now.AddDate(0, 0, -7))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 || rows[0].PromptN != 50 {
		t.Errorf("since filter should keep only the recent event: %+v", rows)
	}

	sum, err := st.Sum("", time.Time{})
	if err != nil {
		t.Fatalf("Sum: %v", err)
	}
	if sum.PromptN != 150 {
		t.Errorf("zero since should return all events, got prompt=%d want 150", sum.PromptN)
	}
	sum7d, err := st.Sum("", now.AddDate(0, 0, -7))
	if err != nil {
		t.Fatalf("Sum: %v", err)
	}
	if sum7d.PromptN != 50 {
		t.Errorf("7d Sum prompt = %d, want 50", sum7d.PromptN)
	}
}

func TestStore_SinceCombinesWithProject(t *testing.T) {
	st := openTemp(t)
	now := time.Now()
	st.Add(Record{Project: "/a", Provider: "kimi", Model: "k2", PromptN: 10, At: now})
	st.Add(Record{Project: "/b", Provider: "kimi", Model: "k2", PromptN: 20, At: now})

	rows, err := st.Query(ByModel, "/a", now.AddDate(0, 0, -1))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 || rows[0].PromptN != 10 {
		t.Errorf("project+since should keep only /a's recent event: %+v", rows)
	}
}

func TestStore_DailyCountsFillsGaps(t *testing.T) {
	st := openTemp(t)
	now := time.Now().UTC()
	if err := st.Add(Record{Project: "/a", Provider: "p", Model: "m", PromptN: 10, PredictedN: 5, At: now.AddDate(0, 0, -1)}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	days, err := st.DailyCounts("", 3)
	if err != nil {
		t.Fatalf("DailyCounts: %v", err)
	}
	if len(days) != 3 {
		t.Fatalf("want 3 day buckets, got %d", len(days))
	}
	// Oldest first: day[0] two days ago (empty), day[1] yesterday (15 tokens),
	// day[2] today (empty).
	if days[0].Tokens != 0 || days[1].Tokens != 15 || days[2].Tokens != 0 {
		t.Errorf("gap days must be zero-filled: %+v", days)
	}
	if days[1].Turns != 1 {
		t.Errorf("yesterday turns = %d, want 1", days[1].Turns)
	}
	for _, d := range days {
		if d.Day.Hour() != 0 || d.Day.Location() != time.UTC {
			t.Errorf("day buckets must be UTC midnight, got %v", d.Day)
		}
	}
}

func TestStore_DailyCountsProjectFilter(t *testing.T) {
	st := openTemp(t)
	now := time.Now().UTC()
	st.Add(Record{Project: "/a", Provider: "p", Model: "m", PromptN: 10, At: now})
	st.Add(Record{Project: "/b", Provider: "p", Model: "m", PromptN: 20, At: now})

	days, err := st.DailyCounts("/a", 1)
	if err != nil {
		t.Fatalf("DailyCounts: %v", err)
	}
	if len(days) != 1 || days[0].Tokens != 10 {
		t.Errorf("project filter should keep only /a: %+v", days)
	}
}
