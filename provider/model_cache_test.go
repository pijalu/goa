// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"testing"
	"time"
)

func TestModelCache_GetSet(t *testing.T) {
	c := NewModelCache()
	models := []ModelInfo{{ID: "m1"}, {ID: "m2"}}
	c.Set("p1", models)

	got, ok := c.Get("p1", time.Minute)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(got) != 2 || got[0].ID != "m1" || got[1].ID != "m2" {
		t.Errorf("got %v, want m1,m2", got)
	}
}

func TestModelCache_TTLExpires(t *testing.T) {
	c := NewModelCache()
	c.Set("p1", []ModelInfo{{ID: "m1"}})

	_, ok := c.Get("p1", -time.Second)
	if ok {
		t.Error("expected expired cache entry to miss")
	}
}

func TestModelCache_Invalidate(t *testing.T) {
	c := NewModelCache()
	c.Set("p1", []ModelInfo{{ID: "m1"}})
	c.Invalidate("p1")

	_, ok := c.Get("p1", time.Minute)
	if ok {
		t.Error("expected invalidated cache entry to miss")
	}
}

func TestModelCache_MissingProvider(t *testing.T) {
	c := NewModelCache()
	_, ok := c.Get("missing", time.Minute)
	if ok {
		t.Error("expected missing provider to miss")
	}
}
