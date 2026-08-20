// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pijalu/goa/internal/agentic"
)

// RoleSessionRecorder persists the COMPLETE event exchange of every pool
// (sub-)agent into per-role JSONL files, mirroring the main agent's session
// store (one JSON-marshaled agentic.OutputEvent per line, flushed per event
// so a crash loses at most the in-flight event).
//
// Rationale (team UI bug RC-6 / logging gap): sub-agent conversations —
// companion reviews, delegated planner/coder work — were invisible to
// exports and post-mortems because only the main session file existed.
//
// Layout: <dir>/<role>.jsonl where dir is resolved lazily per record via
// dirFn (typically .goa/sessions/<mainSessionID>/agents). dirFn returning ""
// (no active session) drops events silently. A dir change (new session)
// closes the role's current writer and opens a fresh file in the new dir.
type RoleSessionRecorder struct {
	dirFn   func() string
	mu      sync.Mutex
	writers map[string]*roleFile
	closed  bool
}

// roleFile owns one open per-role JSONL file.
type roleFile struct {
	dir    string
	f      *os.File
	bw     *bufio.Writer
	broken bool
}

// NewRoleSessionRecorder creates a recorder. dirFn must be cheap and
// concurrency-safe; it is consulted on every record (session IDs rotate on
// /new and session restore, so the directory cannot be captured eagerly).
func NewRoleSessionRecorder(dirFn func() string) *RoleSessionRecorder {
	return &RoleSessionRecorder{
		dirFn:   dirFn,
		writers: map[string]*roleFile{},
	}
}

// Record appends one event to the role's JSONL file. Errors are logged once
// per role and the role's writer is disabled (broken writers must not spam
// the log nor stall the agent's observer loop).
func (r *RoleSessionRecorder) Record(role string, ev agentic.OutputEvent) {
	if r == nil {
		return
	}
	dir := r.dirFn()
	if dir == "" {
		return // no active session: nothing to record into
	}
	rf := r.writerFor(role, dir)
	if rf == nil || rf.broken {
		return
	}
	data, err := json.Marshal(ev)
	if err != nil {
		rf.broken = true
		log.Printf("role session recorder: marshal event for role %q: %v", role, err)
		return
	}
	if _, err := rf.bw.Write(append(data, '\n')); err != nil {
		rf.broken = true
		log.Printf("role session recorder: write role %q: %v", role, err)
		return
	}
	if err := rf.bw.Flush(); err != nil {
		rf.broken = true
		log.Printf("role session recorder: flush role %q: %v", role, err)
	}
}

// writerFor returns (opening on first use) the role's writer for the current
// directory, rotating the file when the session directory changed.
func (r *RoleSessionRecorder) writerFor(role, dir string) *roleFile {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	if rf, ok := r.writers[role]; ok && rf.dir == dir {
		return rf
	}
	// New role or new session directory: close any stale writer first.
	if old, ok := r.writers[role]; ok {
		old.close()
		delete(r.writers, role)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("role session recorder: mkdir %s: %v", dir, err)
		return nil
	}
	path := filepath.Join(dir, sanitizeRoleFileName(role)+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("role session recorder: open %s: %v", path, err)
		return nil
	}
	rf := &roleFile{dir: dir, f: f, bw: bufio.NewWriter(f)}
	r.writers[role] = rf
	return rf
}

// Close flushes and closes all role files. Safe to call multiple times.
func (r *RoleSessionRecorder) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	for role, rf := range r.writers {
		rf.close()
		delete(r.writers, role)
	}
}

// close flushes and closes the file; the caller must hold the recorder lock.
func (rf *roleFile) close() {
	_ = rf.bw.Flush()
	_ = rf.f.Close()
}

// sanitizeRoleFileName maps a role name to a safe single-segment file name:
// lowercased, every run of characters outside [a-z0-9._-] collapsed to one
// '-'. Roles are application-internal (planner, coder, companion, ...) but a
// custom team member can name a role arbitrarily — never trust it as a path.
func sanitizeRoleFileName(role string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(role) {
		safe := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if safe {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	name := strings.Trim(b.String(), "-.")
	if name == "" {
		return "role"
	}
	return name
}
