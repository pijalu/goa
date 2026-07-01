// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package helper

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pijalu/goa/internal/agentic"
)

// Recorder is an OutputObserver that records all LLM interactions to a file.
// This enables replaying conversations for testing without a live LLM.
type Recorder struct {
	mu        sync.Mutex
	events    []RecordedEvent
	session   string
	filePath  string
	startTime time.Time
}

// RecordedEvent represents a single recorded event.
type RecordedEvent struct {
	Timestamp  time.Time             `json:"timestamp"`
	Type       string                `json:"type"`
	Role       string                `json:"role,omitempty"`
	Content    string                `json:"content,omitempty"`
	ToolName   string                `json:"tool_name,omitempty"`
	ToolInput  string                `json:"tool_input,omitempty"`
	ToolResult string                `json:"tool_result,omitempty"`
	State      string                `json:"state,omitempty"`
	Timings    *agentic.TokenTimings `json:"timings,omitempty"`
}

// NewRecorder creates a new recorder that will save to the specified directory.
func NewRecorder(sessionName string, directory string) *Recorder {
	if directory == "" {
		directory = "."
	}

	// Ensure directory exists
	if err := os.MkdirAll(directory, 0755); err != nil {
		return nil
	}

	filePath := filepath.Join(directory, fmt.Sprintf("%s.jsonl", sessionName))

	return &Recorder{
		session:   sessionName,
		filePath:  filePath,
		startTime: time.Now(),
	}
}

// OnEvent implements agentic.OutputObserver interface.
func (r *Recorder) OnEvent(event agentic.OutputEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()

	recorded := RecordedEvent{
		Timestamp:  time.Now(),
		Type:       string(event.Type),
		Role:       string(event.Role),
		Content:    event.Text,
		ToolName:   event.ToolName,
		ToolInput:  event.ToolInput,
		ToolResult: event.ToolResult,
		State:      event.State.String(),
		Timings:    event.Timings,
	}

	r.events = append(r.events, recorded)
}

// Save writes the recorded events to the file.
func (r *Recorder) Save() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	file, err := os.Create(r.filePath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	for _, event := range r.events {
		if err := encoder.Encode(event); err != nil {
			return fmt.Errorf("encode event: %w", err)
		}
	}

	return nil
}

// Load reads recorded events from a file.
func (r *Recorder) Load(filePath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	r.events = nil
	r.filePath = filePath

	decoder := json.NewDecoder(file)
	for decoder.More() {
		var event RecordedEvent
		if err := decoder.Decode(&event); err != nil {
			return fmt.Errorf("decode event: %w", err)
		}
		r.events = append(r.events, event)
	}

	return nil
}

// Events returns a copy of the recorded events.
func (r *Recorder) Events() []RecordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()

	result := make([]RecordedEvent, len(r.events))
	copy(result, r.events)
	return result
}

// Session returns the session name.
func (r *Recorder) Session() string {
	return r.session
}

// FilePath returns the path to the recorded file.
func (r *Recorder) FilePath() string {
	return r.filePath
}

// Duration returns the recording duration.
func (r *Recorder) Duration() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.startTime.IsZero() {
		return 0
	}
	return time.Since(r.startTime)
}

// Clear resets the recorder.
func (r *Recorder) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.events = nil
	r.startTime = time.Now()
}
