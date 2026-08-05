// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"crypto/sha256"
	"fmt"
	"sync"
)

// readDedupCapacity bounds the dedup hash ring. It is deliberately small: the
// ring only needs to cover the files a session actively revisits, not the
// whole project. When full, the oldest hash is evicted (circular), so a file
// whose hash aged out is simply returned in full once more.
const readDedupCapacity = 64

// readDedupStore is a circular buffer of sha256 hashes of rendered read
// results. It implements the E1.3 dedup (ENHANCE.md): re-reading
// byte-identical content returns a short hint instead of the full body, so an
// append-only context is not bloated with redundant file copies. Keying on
// the RENDERED content (not the path) means a modified file or a different
// line range hashes differently and is correctly returned in full.
//
// The store is safe for concurrent use: one ReadFileTool instance is shared
// across the agent and any orchestrator subagents, which may read in
// parallel.
type readDedupStore struct {
	mu     sync.Mutex
	hashes [readDedupCapacity][sha256.Size]byte
	head   int // next slot to overwrite (ring pointer)
	count  int // number of valid entries, capped at capacity
}

// seenOrAdd reports whether h is already in the ring; if not, it appends h
// (evicting the oldest entry when full) and returns false.
func (s *readDedupStore) seenOrAdd(h [sha256.Size]byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.count
	if n > readDedupCapacity {
		n = readDedupCapacity
	}
	for i := 0; i < n; i++ {
		if s.hashes[i] == h {
			return true
		}
	}
	s.hashes[s.head] = h
	s.head = (s.head + 1) % readDedupCapacity
	s.count++
	return false
}

// dedupHint renders the short notice returned in place of file content when a
// read is deduped. It tells the model the content is unchanged and how to get
// a specific portion if it genuinely needs it again.
func dedupHint(path string, totalLines, totalBytes int) string {
	return fmt.Sprintf("[dedup] %s unchanged since a previous read this session (%d lines, %d bytes) — content omitted to save context. Use start_line/end_line for a specific range, or edit/search the file if you need its current state.",
		shortenPath(path), totalLines, totalBytes)
}

// hashRenderedRead computes the dedup key for a rendered read result.
func hashRenderedRead(rendered string) [sha256.Size]byte {
	return sha256.Sum256([]byte(rendered))
}
