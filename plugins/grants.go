// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// GrantHook is one user-approved (point, mode) pair from a plugin's manifest.
type GrantHook struct {
	Point string `json:"point"`
	Mode  string `json:"mode"`
}

// PluginGrant records the user's install-time acceptance for one external
// plugin (plugins plan §7 step 2). Grants are keyed by plugin id inside
// <managerRoot>/grants.json and carry the version + manifest fingerprint they
// were granted for: when either drifts (the plugin adds or changes hooks, or
// bumps its version), the grant goes stale and its hooks require re-approval.
type PluginGrant struct {
	Version       string      `json:"version"`
	ManifestHash  string      `json:"manifest_hash"`
	ApprovedHooks []GrantHook `json:"approved_hooks"`
	ApprovedAt    time.Time   `json:"approved_at"`
}

// GrantStore persists hook approvals as one JSON document with 0600
// permissions (matching StorageBridge — grants gate code execution paths, so
// they are security-relevant state). Writes are atomic tmp+rename so a crash
// mid-write cannot corrupt approvals. All methods are safe for concurrent use.
//
// Failure policy is fail-closed by construction: any read problem surfaces as
// "no grant", which forces the approval prompt again rather than silently
// running unreviewed hooks.
type GrantStore struct {
	mu   sync.Mutex
	path string
}

// NewGrantStore creates the grant store rooted at dir (the plugin manager
// root); the backing file lives at dir/grants.json.
func NewGrantStore(dir string) *GrantStore {
	return &GrantStore{path: filepath.Join(dir, "grants.json")}
}

// Path returns the backing file location (tests/diagnostics).
func (s *GrantStore) Path() string { return s.path }

// Get loads the grant for pluginID. ok=false means absent; a corrupt or
// unreadable file also reads as absent (with err set for logging), never as
// an implicit approval.
func (s *GrantStore) Get(pluginID string) (PluginGrant, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.loadLocked()
	if err != nil {
		return PluginGrant{}, false, err
	}
	g, ok := all[pluginID]
	return g, ok, nil
}

// Approve records or overwrites the grant for pluginID, preserving other
// plugins' entries. Refuses to write when the existing file cannot be parsed:
// clobbering unreadable state could silently drop other plugins' grants.
func (s *GrantStore) Approve(pluginID string, g PluginGrant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.loadLocked()
	if err != nil {
		return fmt.Errorf("refusing to overwrite unreadable grants: %w", err)
	}
	all[pluginID] = g
	return s.saveLocked(all)
}

// Revoke removes the grant for pluginID. Missing entries are a no-op.
// An unreadable file is RESET to a clean empty document: there is no
// trustworthy state to preserve, and failing to reset would leave the store
// permanently wedged (every future Approve refuses, every Get reads as
// no-grant). Resetting is the fail-closed-but-recoverable choice.
func (s *GrantStore) Revoke(pluginID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.loadLocked()
	if err != nil {
		return s.saveLocked(map[string]PluginGrant{})
	}
	if _, ok := all[pluginID]; !ok {
		return nil
	}
	delete(all, pluginID)
	return s.saveLocked(all)
}

// grantsFile is the on-disk document shape ({pluginID: grant}).
type grantsFile struct {
	Grants map[string]PluginGrant `json:"grants"`
}

// loadLocked parses the backing file. A missing file yields an empty map and
// no error; anything else that cannot be parsed yields an error (callers
// treat it as "no grants" + log). Caller must hold s.mu.
func (s *GrantStore) loadLocked() (map[string]PluginGrant, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]PluginGrant{}, nil
		}
		return nil, fmt.Errorf("read grants: %w", err)
	}
	var doc grantsFile
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("corrupt grants %s: %w", s.path, err)
	}
	if doc.Grants == nil {
		doc.Grants = map[string]PluginGrant{}
	}
	return doc.Grants, nil
}

// saveLocked writes data atomically with 0600 permissions (StorageBridge
// pattern): tmp file + rename inside the same directory. Caller holds s.mu.
func (s *GrantStore) saveLocked(all map[string]PluginGrant) error {
	raw, err := json.MarshalIndent(grantsFile{Grants: all}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode grants: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write grants: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("commit grants: %w", err)
	}
	return nil
}

// ManifestFingerprint hashes a plugin's declared hook list into the grant
// staleness key (§7 step 2). Declarations are sorted by (mode, point) first,
// so YAML list reordering — pure noise — does NOT invalidate grants, while
// adding, removing, or changing any declared hook does.
func ManifestFingerprint(hooks []PluginHookDecl) string {
	lines := make([]string, 0, len(hooks))
	for _, h := range hooks {
		lines = append(lines, h.Mode+"\x00"+h.Point)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// NewPluginGrant builds a grant for def covering approved pairs. The
// fingerprint and version snapshot come from the manifest being reviewed, so
// later drift detection compares like with like.
func NewPluginGrant(def *PluginDef, approved []GrantHook) PluginGrant {
	return PluginGrant{
		Version:       def.Version,
		ManifestHash:  ManifestFingerprint(def.Hooks),
		ApprovedHooks: approved,
		ApprovedAt:    time.Now(),
	}
}

// GrantStale reports whether an existing grant still covers def's CURRENT
// manifest (§7 step 2 staleness rule): a missing grant, a version bump, or a
// changed hook fingerprint each force re-approval ("re-prompt").
func GrantStale(g PluginGrant, exists bool, def *PluginDef) bool {
	return !exists ||
		g.Version != def.Version ||
		g.ManifestHash != ManifestFingerprint(def.Hooks)
}

// RequiresReview reports whether a manifest's declarations warrant the
// install-time review card: any declared hook, or any permission (the known
// permission set is exactly the sensitive trio provider-keys / ui-confirm /
// account-write).
func RequiresReview(def *PluginDef) bool {
	return len(def.Hooks) > 0 || len(def.Permissions) > 0
}
