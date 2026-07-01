// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package sandbox provides defence-in-depth isolation for local code tools.
//
// It is not a container boundary: it hardens subprocess execution by
// repointing HOME/TMPDIR, stripping credentials, applying rlimits, and
// enforcing a command-position blocklist.  This matches the Unsloth Studio
// sandbox model.
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	// defaultSessionID is used when no session identifier is provided.
	defaultSessionID = "_default"
	// invalidSessionID is used when an unsafe session ID is supplied.
	invalidSessionID = "_invalid"
)

// sessionIDRE limits session identifiers to safe filename characters.
var sessionIDRE = regexp.MustCompile(`\A[A-Za-z0-9_\-]{1,64}\z`)

// Manager allocates and tracks per-session sandbox directories.
type Manager struct {
	root        string
	worktreeMgr WorktreeResolver
	workdirs    map[string]string
}

// WorktreeResolver resolves a project-bound sandbox path for a session.
type WorktreeResolver interface {
	ProjectDir() string
}

// NewManager creates a sandbox manager rooted at root.
// If root is empty, ~/.goa/sandbox is used.
func NewManager(root string, worktreeMgr WorktreeResolver) (*Manager, error) {
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("sandbox: cannot resolve home dir: %w", err)
		}
		root = filepath.Join(home, ".goa", "sandbox")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("sandbox: create root: %w", err)
	}
	return &Manager{
		root:        root,
		worktreeMgr: worktreeMgr,
		workdirs:    make(map[string]string),
	}, nil
}

// Workdir returns a per-session sandbox directory at mode 0o700.
func (m *Manager) Workdir(sessionID string) (string, error) {
	key := sessionID
	if key == "" {
		key = defaultSessionID
	}

	if cached, ok := m.workdirs[key]; ok {
		if info, err := os.Stat(cached); err == nil && info.IsDir() {
			return cached, nil
		}
	}

	dir, err := m.resolveWorkdir(sessionID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("sandbox: create workdir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("sandbox: chmod workdir: %w", err)
	}
	m.workdirs[key] = dir
	return dir, nil
}

func (m *Manager) resolveWorkdir(sessionID string) (string, error) {
	if sessionID == "" {
		return filepath.Join(m.root, defaultSessionID), nil
	}
	if !sessionIDRE.MatchString(sessionID) {
		return filepath.Join(m.root, invalidSessionID), nil
	}
	return filepath.Join(m.root, sessionID), nil
}

// Purge removes sandbox directories older than ttl.
func (m *Manager) Purge(ttl time.Duration) error {
	entries, err := os.ReadDir(m.root)
	if err != nil {
		return fmt.Errorf("sandbox: read root: %w", err)
	}
	cutoff := time.Now().Add(-ttl)
	var lastErr error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			lastErr = err
			continue
		}
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(m.root, e.Name())
			if err := os.RemoveAll(path); err != nil {
				lastErr = err
			}
		}
	}
	return lastErr
}

// ValidateSessionID reports whether id is safe to use as a directory name.
func ValidateSessionID(id string) bool {
	return id != "" && sessionIDRE.MatchString(id) && !strings.Contains(id, "..")
}
