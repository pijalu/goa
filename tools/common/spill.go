// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package common

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SpillStore is the session-scoped generalization of SaveTruncatedOutput:
// instead of an unscoped os.CreateTemp file, oversized content is written
// verbatim into a private per-session directory (~/.goa/spill/<session>/)
// with owner-only permissions, so a spilled tool result stays attributable
// to the session that produced it and unreadable by other local users.
type SpillStore struct {
	dir string
}

// NewSpillStore returns a SpillStore writing into dir (created on first Save
// with 0700 permissions). An empty dir makes every Save fail so callers fall
// back to keeping content inline.
func NewSpillStore(dir string) *SpillStore {
	return &SpillStore{dir: dir}
}

// Dir reports the session-scoped directory this store writes into.
func (s *SpillStore) Dir() string {
	return s.dir
}

// SessionSpillDir returns the spill directory for a session under the goa
// state root: <goaDir>/spill/<session>. The session ID is untrusted (it can
// come from a restored session name), so it is sanitized to a single safe
// path segment.
func SessionSpillDir(goaDir, sessionID string) string {
	return filepath.Join(goaDir, "spill", sanitizeSpillSegment(sessionID))
}

// Save writes content verbatim to a fresh file under the session directory
// and returns its path. The filename is a random hex prefix plus the
// sanitized suggested name (e.g. "bash.txt"), so concurrent spills never
// collide and a pre-planted file cannot be redirected: the create is
// exclusive (O_EXCL) with owner-only permissions (0600).
func (s *SpillStore) Save(suggestedName, content string) (string, error) {
	if s.dir == "" {
		return "", fmt.Errorf("spill store has no directory")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", err
	}
	name := sanitizeSpillSegment(suggestedName)
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	path := filepath.Join(s.dir, hex.EncodeToString(suffix[:])+"-"+name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// sanitizeSpillSegment reduces an untrusted string to a single filesystem-safe
// path segment: [A-Za-z0-9._-] are kept, everything else becomes '_', and the
// traversal tokens "." and ".." are replaced outright. An empty result maps to
// "spill" so the segment is never blank.
func sanitizeSpillSegment(raw string) string {
	if raw == "" {
		return "spill"
	}
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "." || out == ".." {
		return strings.ReplaceAll(out, ".", "_")
	}
	return out
}
