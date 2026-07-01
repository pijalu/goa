// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tasks

import (
	"testing"

	"github.com/pijalu/goa/internal/event"
)

func TestBusLifecycle(t *testing.T) {
	bus := NewBus(NopStore{}, event.MakeBus(10, 10, 10, 10))
	task := bus.Register("t1", "agent", "main", "do work")
	if task.Status != StatusPending {
		t.Errorf("status = %v, want pending", task.Status)
	}

	bus.Start("t1")
	if got, _ := bus.Get("t1"); got.Status != StatusRunning {
		t.Errorf("status = %v, want running", got.Status)
	}

	bus.Complete("t1", "done")
	if got, _ := bus.Get("t1"); got.Status != StatusCompleted || got.Result != "done" {
		t.Errorf("complete = %v/%v", got.Status, got.Result)
	}
}

func TestBusActive(t *testing.T) {
	bus := NewBus(NopStore{}, nil)
	bus.Register("t1", "agent", "main", "a")
	bus.Register("t2", "agent", "main", "b")
	bus.Complete("t1", "ok")

	active := bus.Active()
	if len(active) != 1 || active[0].ID != "t2" {
		t.Errorf("active = %v, want [t2]", active)
	}
}

func TestBusFail(t *testing.T) {
	bus := NewBus(NopStore{}, nil)
	bus.Register("t1", "agent", "main", "a")
	bus.Fail("t1", "boom")
	if got, _ := bus.Get("t1"); got.Status != StatusFailed || got.Error != "boom" {
		t.Errorf("fail = %v/%v", got.Status, got.Error)
	}
}

func TestBusCancelUnknown(t *testing.T) {
	bus := NewBus(NopStore{}, nil)
	if got := bus.Cancel("missing"); got != nil {
		t.Error("cancel unknown should return nil")
	}
}

func TestBusEventEmit(t *testing.T) {
	eb := event.MakeBus(10, 10, 10, 10)
	bus := NewBus(NopStore{}, eb)
	bus.Register("t1", "agent", "main", "do work")

	select {
	case ev := <-eb.Chat:
		if ev.TaskUpdate == nil || ev.TaskUpdate.TaskID != "t1" {
			t.Errorf("unexpected event: %+v", ev)
		}
	default:
		t.Error("expected task update event")
	}
}
