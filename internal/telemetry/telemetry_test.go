// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package telemetry

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

func TestClientDisabled(t *testing.T) {
	c := NewClient(false, t.TempDir())
	c.Record("test", nil)
	if c.Enabled() {
		t.Error("expected disabled")
	}
}

func TestClientFlush(t *testing.T) {
	dir := t.TempDir()
	c := NewClient(true, dir)
	c.Record("event1", map[string]string{"k": "v"})
	if err := c.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	f, err := os.Open(c.storePath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if e.Name != "event1" {
			t.Errorf("name = %s", e.Name)
		}
		count++
	}
	if count != 1 {
		t.Errorf("events = %d", count)
	}
}
