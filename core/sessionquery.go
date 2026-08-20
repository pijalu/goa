// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/agentic"
)

// ListSessionIDs returns the ids (file names without the .jsonl suffix) of
// all persisted session files, including abandoned/empty ones, ordered newest
// first by file modification time. Missing sessions dir yields an empty list.
func (s *SessionStore) ListSessionIDs() ([]string, error) {
	sessionDir := filepath.Join(s.dir, "sessions")
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(entry.Name(), ".jsonl"))
	}

	sort.Slice(ids, func(i, j int) bool {
		ti, _ := s.SessionModifiedTime(ids[i])
		tj, _ := s.SessionModifiedTime(ids[j])
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return ids[i] > ids[j]
	})
	return ids, nil
}

// SessionModifiedTime returns the modification time of a persisted session
// file. The second result is false when the session does not exist.
func (s *SessionStore) SessionModifiedTime(sessionID string) (time.Time, bool) {
	info, err := os.Stat(s.sessionFilePath(sessionID))
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

// sessionFilePath resolves a session id to its JSONL path, rejecting ids that
// would escape the sessions directory (path traversal).
func (s *SessionStore) sessionFilePath(sessionID string) string {
	// Guard: session ids are opaque tokens; anything with a separator or a
	// parent reference must never become a filesystem path.
	if sessionID == "" || strings.ContainsAny(sessionID, `/\`) || sessionID == ".." {
		return ""
	}
	return filepath.Join(s.dir, "sessions", sessionID+".jsonl")
}

// ScanSessionEvents streams the events of a persisted session file in order.
// visit is called with the 1-based event sequence number (line number) and
// the parsed event. The scan stops early when visit returns false. Lines that
// fail to parse are counted in the sequence number but not visited. Returns
// the number of non-empty lines scanned. Returns an error when the session
// does not exist.
func (s *SessionStore) ScanSessionEvents(sessionID string, visit func(seq int, ev agentic.OutputEvent) bool) (int, error) {
	path := s.sessionFilePath(sessionID)
	if path == "" {
		return 0, os.ErrNotExist
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Session lines can be large (big tool results); allow long lines.
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)

	seq := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		seq++
		var ev agentic.OutputEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if !visit(seq, ev) {
			break
		}
	}
	return seq, scanner.Err()
}
