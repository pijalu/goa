// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal"
)

// memScheduleStore is an in-memory tools.ScheduleStore used to test the tools
// without the app-level persistent store.
type memScheduleStore struct {
	seen []string
	jobs []ScheduleJob
}

func (m *memScheduleStore) Create(job ScheduleJob) (ScheduleJob, error) {
	job.ID = "schedule-" + itoa(len(m.seen)+1)
	m.seen = append(m.seen, job.ID)
	job.CreatedAt = time.Now()
	m.jobs = append(m.jobs, job)
	return job, nil
}

func (m *memScheduleStore) Delete(id string) (bool, error) {
	for i, job := range m.jobs {
		if job.ID == id {
			m.jobs = append(m.jobs[:i], m.jobs[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func (m *memScheduleStore) List() ([]ScheduleJob, error) {
	out := make([]ScheduleJob, len(m.jobs))
	copy(out, m.jobs)
	return out, nil
}

func (m *memScheduleStore) TakeDue(now time.Time) ([]ScheduleJob, error) {
	var due []ScheduleJob
	var remaining []ScheduleJob
	for _, job := range m.jobs {
		if job.ScheduledAt.After(now) {
			remaining = append(remaining, job)
			continue
		}
		due = append(due, job)
		if job.IsOneShot() {
			continue
		}
		job.ScheduledAt = job.ScheduledAt.Add(time.Duration(job.EverySeconds) * time.Second)
		remaining = append(remaining, job)
	}
	m.jobs = remaining
	return due, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// createTool builds a create tool over a fresh in-memory store.
func createTool(t *testing.T) (*ScheduleCreateTool, *memScheduleStore) {
	t.Helper()
	store := &memScheduleStore{}
	return &ScheduleCreateTool{Store: store}, store
}

func TestScheduleCreate_AfterSeconds_RoundTrip(t *testing.T) {
	tool, store := createTool(t)
	out, err := tool.Execute(`{"prompt": "commit before leaving", "after_seconds": 3600}`)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(out, "schedule-1") {
		t.Fatalf("expected id schedule-1 in output, got %s", out)
	}
	if !strings.Contains(out, `"state": "scheduled"`) {
		t.Fatalf("expected scheduled state, got %s", out)
	}
	if len(store.jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(store.jobs))
	}
	if store.jobs[0].Kind != ScheduleKindAfter || store.jobs[0].AfterSeconds != 3600 {
		t.Fatalf("unexpected job: %+v", store.jobs[0])
	}
}

func TestScheduleCreate_ExactlyOneSelector(t *testing.T) {
	tool, _ := createTool(t)
	cases := []string{
		`{"prompt": "x"}`, // no selector
		`{"prompt": "x", "after_seconds": 1, "at": "2099-01-01T00:00:00Z"}`, // two selectors
		`{"prompt": "x", "after_seconds": 1, "every_seconds": 300}`,         // two selectors
	}
	for _, input := range cases {
		_, err := tool.Execute(input)
		if err == nil {
			t.Fatalf("expected error for %s", input)
		}
		if te, ok := err.(*internal.ToolError); !ok || te.Type != "invalid_selector" {
			t.Fatalf("expected invalid_selector for %s, got %v", input, err)
		}
	}
}

func TestScheduleCreate_Validation(t *testing.T) {
	tool, _ := createTool(t)
	cases := []struct {
		input string
		typ   string
	}{
		{`{"prompt": "   ", "after_seconds": 1}`, "invalid_prompt"},
		{`{"prompt": "x", "after_seconds": 0}`, "invalid_rule"},
		{`{"prompt": "x", "every_seconds": 60}`, "frequency_too_high"},
		{`{"prompt": "x", "at": "not-a-time"}`, "invalid_rule"},
		{`{"prompt": "x", "at": "2000-01-01T00:00:00Z"}`, "not_future"},
		{`{"prompt": "x", "at": {"date": "2099-01-01", "time": "10:00:00", "time_zone": "Not/AZone"}}`, "invalid_time_zone"},
		{`{"prompt": "x", "at": {"date": "2099-01-01", "time": "10:00:00"}}`, "invalid_rule"},
	}
	for _, c := range cases {
		_, err := tool.Execute(c.input)
		if err == nil {
			t.Fatalf("expected error for %s", c.input)
		}
		te, ok := err.(*internal.ToolError)
		if !ok {
			t.Fatalf("expected ToolError for %s, got %T: %v", c.input, err, err)
		}
		if te.Type != c.typ {
			t.Fatalf("for %s: expected type %s, got %s (%v)", c.input, c.typ, te.Type, err)
		}
	}
}

func TestScheduleCreate_AtLocalObject(t *testing.T) {
	tool, store := createTool(t)
	out, err := tool.Execute(`{"prompt": "noon check", "at": {"date": "2099-01-01", "time": "10:00", "time_zone": "UTC"}}`)
	if err != nil {
		t.Fatalf("create local at: %v", err)
	}
	if !strings.Contains(out, "2099-01-01T10:00:00Z") {
		t.Fatalf("expected UTC-noon target in output, got %s", out)
	}
	if store.jobs[0].Kind != ScheduleKindAt {
		t.Fatalf("expected kind at, got %s", store.jobs[0].Kind)
	}
}

func TestScheduleCreate_AtRFC3339String(t *testing.T) {
	tool, store := createTool(t)
	out, err := tool.Execute(`{"prompt": "release", "at": "2099-06-01T12:00:00Z"}`)
	if err != nil {
		t.Fatalf("create at string: %v", err)
	}
	if !strings.Contains(out, "2099-06-01T12:00:00Z") {
		t.Fatalf("expected target in output, got %s", out)
	}
	if store.jobs[0].Kind != ScheduleKindAt {
		t.Fatalf("expected kind at, got %s", store.jobs[0].Kind)
	}
}

func TestScheduleCreate_EveryRecurrence(t *testing.T) {
	tool, store := createTool(t)
	out, err := tool.Execute(`{"prompt": "heartbeat", "every_seconds": 600}`)
	if err != nil {
		t.Fatalf("create every: %v", err)
	}
	if !strings.Contains(out, `"everySeconds": 600`) {
		t.Fatalf("expected everySeconds in output, got %s", out)
	}
	if store.jobs[0].Kind != ScheduleKindEvery || store.jobs[0].EverySeconds != 600 {
		t.Fatalf("unexpected job: %+v", store.jobs[0])
	}
}

func TestScheduleDelete_RoundTrip(t *testing.T) {
	create, store := createTool(t)
	if _, err := create.Execute(`{"prompt": "x", "after_seconds": 1}`); err != nil {
		t.Fatalf("create: %v", err)
	}
	del := &ScheduleDeleteTool{Store: store}
	out, err := del.Execute(`{"id": "schedule-1"}`)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(out, `"deleted": true`) {
		t.Fatalf("expected deleted true, got %s", out)
	}
	if len(store.jobs) != 0 {
		t.Fatalf("expected empty store after delete, got %d jobs", len(store.jobs))
	}
	// Deleting an unknown id returns deleted false with schedule_not_found.
	out, err = del.Execute(`{"id": "schedule-1"}`)
	if err != nil {
		t.Fatalf("delete unknown: %v", err)
	}
	if !strings.Contains(out, `"deleted": false`) || !strings.Contains(out, "schedule_not_found") {
		t.Fatalf("expected deleted false + schedule_not_found, got %s", out)
	}
}

func TestScheduleList_RoundTrip(t *testing.T) {
	create, store := createTool(t)
	for i := 0; i < 3; i++ {
		if _, err := create.Execute(`{"prompt": "job ` + itoa(i) + `", "after_seconds": 1}`); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	list := &ScheduleListTool{Store: store}
	out, err := list.Execute(`{}`)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var views []map[string]any
	if err := json.Unmarshal([]byte(out), &views); err != nil {
		t.Fatalf("list output not JSON array: %v\n%s", err, out)
	}
	if len(views) != 3 {
		t.Fatalf("expected 3 views, got %d", len(views))
	}
	if views[0]["id"] != "schedule-1" || views[2]["id"] != "schedule-3" {
		t.Fatalf("expected creation order schedule-1..3, got %v %v", views[0]["id"], views[2]["id"])
	}
}

func TestRenderDueReminders_Framing(t *testing.T) {
	due := []ScheduleJob{
		{ID: "schedule-1", Prompt: `say "hi"`, ScheduledAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
	}
	framing := RenderDueReminders(due)
	if !strings.HasPrefix(framing, "[SCHEDULE REMINDER]") {
		t.Fatalf("expected header, got %s", framing)
	}
	if !strings.Contains(framing, "untrusted reminder content") {
		t.Fatalf("expected untrusted-content guard, got %s", framing)
	}
	if !strings.Contains(framing, `"reminder_prompt":"say \"hi\""`) {
		t.Fatalf("expected JSON-escaped prompt in framing, got %s", framing)
	}
}

func TestScheduleTools_Documentable(t *testing.T) {
	for _, tool := range []Documentable{&ScheduleCreateTool{}, &ScheduleDeleteTool{}, &ScheduleListTool{}} {
		if tool.ShortDoc() == "" {
			t.Errorf("%T: empty ShortDoc", tool)
		}
		if tool.LongDoc() == "" {
			t.Errorf("%T: empty LongDoc", tool)
		}
		if len(tool.Examples()) == 0 {
			t.Errorf("%T: no examples", tool)
		}
	}
}

func TestScheduleTools_SchemaNames(t *testing.T) {
	if got := (&ScheduleCreateTool{}).Schema().Name; got != "schedule_create" {
		t.Fatalf("create schema name = %s", got)
	}
	if got := (&ScheduleDeleteTool{}).Schema().Name; got != "schedule_delete" {
		t.Fatalf("delete schema name = %s", got)
	}
	if got := (&ScheduleListTool{}).Schema().Name; got != "schedule_list" {
		t.Fatalf("list schema name = %s", got)
	}
}
