// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package telemetry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event represents an anonymous usage event.
type Event struct {
	Name      string            `json:"name"`
	Timestamp time.Time         `json:"timestamp"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// Client records anonymous events.
type Client struct {
	mu        sync.Mutex
	enabled   bool
	queue     []Event
	flushAt   int
	storePath string
}

// NewClient creates a telemetry client.
func NewClient(enabled bool, storeDir string) *Client {
	return &Client{
		enabled:   enabled,
		flushAt:   10,
		storePath: filepath.Join(storeDir, "telemetry.jsonl"),
	}
}

// Record queues an event if telemetry is enabled.
func (c *Client) Record(name string, metadata map[string]string) {
	if !c.enabled || c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queue = append(c.queue, Event{
		Name:      name,
		Timestamp: time.Now(),
		Metadata:  metadata,
	})
	if len(c.queue) >= c.flushAt {
		_ = c.flushLocked()
	}
}

// Flush persists queued events.
func (c *Client) Flush() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.flushLocked()
}

func (c *Client) flushLocked() error {
	if len(c.queue) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.storePath), 0o755); err != nil {
		return fmt.Errorf("mkdir telemetry: %w", err)
	}
	f, err := os.OpenFile(c.storePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range c.queue {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	c.queue = c.queue[:0]
	return nil
}

// Enabled reports whether telemetry is enabled.
func (c *Client) Enabled() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enabled
}

// SetEnabled toggles telemetry.
func (c *Client) SetEnabled(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.enabled = enabled
}
