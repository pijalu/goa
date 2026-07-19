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

	byProv, err := st.Query(ByProvider, "")
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
	got, err := st.Query(ByModel, "/b")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 || got[0].Key != "glm" {
		t.Fatalf("per-project ByModel = %+v, want only glm", got)
	}

	// Global: both models.
	all, err := st.Query(ByModel, "")
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

	sum, err := st.Sum("")
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
	got, err := st.Query(ByProject, "")
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
