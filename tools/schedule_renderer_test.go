// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/tuirender"
)

func TestScheduleRenderer_RenderCall(t *testing.T) {
	r := ScheduleRenderer{}
	ctx := tuirender.RenderContext{}

	create := r.RenderCall(map[string]any{"prompt": "commit before leaving"}, ctx)
	if !strings.Contains(create, "Create reminder") || !strings.Contains(create, "commit before leaving") {
		t.Fatalf("create call header: %q", create)
	}

	del := r.RenderCall(map[string]any{"id": "schedule-1"}, ctx)
	if !strings.Contains(del, "Delete reminder") || !strings.Contains(del, "schedule-1") {
		t.Fatalf("delete call header: %q", del)
	}

	list := r.RenderCall(map[string]any{}, ctx)
	if !strings.Contains(list, "List reminders") {
		t.Fatalf("list call header: %q", list)
	}
}

func TestScheduleRenderer_RenderResult(t *testing.T) {
	r := ScheduleRenderer{}
	ctx := tuirender.RenderContext{}
	out := r.RenderResult(`{"id": "schedule-1", "deleted": true}`, ctx)
	if !strings.Contains(out, "schedule-1") {
		t.Fatalf("result: %q", out)
	}
	if r.PreviewLines() <= 0 {
		t.Fatalf("expected positive preview lines")
	}
}
