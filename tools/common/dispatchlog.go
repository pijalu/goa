// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package common

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"
)

// DispatchEntry is one durable record of a single tool sub-call dispatched
// from a run_code program (gap TL7, dsh code-mode parity). Each entry carries
// everything needed to reconstruct the nested execution after the fact: the
// outer run id, the sub-call's sequence and call id, the tool name, the exact
// arguments dispatched, timing, and the (spill-capped) outcome.
type DispatchEntry struct {
	RunID      string    `json:"run_id"`
	Seq        int       `json:"seq"`
	CallID     string    `json:"call_id"`
	Tool       string    `json:"tool"`
	Arguments  string    `json:"arguments"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	DurationMS int64     `json:"duration_ms"`
	OK         bool      `json:"ok"`
	Result     string    `json:"result,omitempty"`
	Error      string    `json:"error,omitempty"`
	SpillPath  string    `json:"spill_path,omitempty"`
}

// DispatchLog is a durable, append-only JSONL log of sub-call dispatches. One
// DispatchLog instance owns a single file: the caller creates a fresh log per
// run_code run (one file per outer run). Every Append is a single write plus an
// fsync, so a completed sub-call survives a crash of the host process.
//
// A DispatchLog is safe for concurrent use; the mutex serializes appends so
// interleaved writes never corrupt a JSON line.
type DispatchLog struct {
	mu   sync.Mutex
	f    *os.File
	path string
}

// NewDispatchLog opens (creating if needed) the JSONL dispatch log at path
// with owner-only permissions and append semantics.
func NewDispatchLog(path string) (*DispatchLog, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &DispatchLog{f: f, path: path}, nil
}

// Path returns the filesystem path of the log file.
func (l *DispatchLog) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Append durably records one sub-call dispatch as a single JSON line. The
// write and fsync happen under the mutex; on any error the entry is dropped
// (callers treat the log as best-effort diagnostics that must never fail the
// sub-call itself).
func (l *DispatchLog) Append(e DispatchEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return errors.New("dispatch log is closed")
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := l.f.Write(data); err != nil {
		return err
	}
	return l.f.Sync()
}

// Close closes the log file. Append after Close returns an error. Close is
// idempotent.
func (l *DispatchLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
