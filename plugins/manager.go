// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pijalu/goa/internal/trust"
)

// Manager installs, removes, and tracks plugins from git repositories. All
// methods are safe for concurrent use.
type Manager struct {
	mu        sync.RWMutex
	root      string
	lock      *Lockfile
	trust     *trust.Manager
	enabled   map[string]bool
	cloneFunc func(url, dir string) error
}

// NewManager creates a plugin manager. root is the directory where plugins are
// installed. The lockfile is stored at root/plugin.lock.
func NewManager(root string, trustMgr *trust.Manager) (*Manager, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir plugin root: %w", err)
	}
	lock := NewLockfile(filepath.Join(root, "plugin.lock"))
	if err := lock.Load(); err != nil {
		return nil, fmt.Errorf("load lockfile: %w", err)
	}
	return &Manager{
		root:      root,
		lock:      lock,
		trust:     trustMgr,
		enabled:   make(map[string]bool),
		cloneFunc: runGitClone,
	}, nil
}

// Install clones a plugin from a git URL, validates its manifest, computes
// a content hash, and records it in the lockfile. The plugin is installed but
// not enabled until the user explicitly activates it.
func (m *Manager) Install(source string) (string, error) {
	if err := validateSource(source); err != nil {
		return "", err
	}

	tmpDir, err := os.MkdirTemp("", "goa-plugin-*")
	if err != nil {
		return "", fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	cloneDir := filepath.Join(tmpDir, "plugin")
	if err := m.cloneFunc(source, cloneDir); err != nil {
		return "", fmt.Errorf("clone: %w", err)
	}

	if err := ValidateManifest(filepath.Join(cloneDir, "plugin.yaml")); err != nil {
		return "", fmt.Errorf("manifest: %w", err)
	}

	def, err := loadManifest(filepath.Join(cloneDir, "plugin.yaml"))
	if err != nil {
		return "", fmt.Errorf("load manifest: %w", err)
	}
	if def.ID == "" {
		return "", fmt.Errorf("plugin manifest missing id")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.lock.Get(def.ID); exists {
		return "", fmt.Errorf("plugin %s already installed", def.ID)
	}

	targetDir := filepath.Join(m.root, def.ID)
	if err := os.RemoveAll(targetDir); err != nil {
		return "", fmt.Errorf("remove old plugin dir: %w", err)
	}
	if err := moveDir(cloneDir, targetDir); err != nil {
		return "", fmt.Errorf("move plugin: %w", err)
	}

	hash, err := hashPluginDir(targetDir)
	if err != nil {
		return "", fmt.Errorf("hash plugin: %w", err)
	}

	entry := LockEntry{
		ID:      def.ID,
		Source:  source,
		Version: def.Version,
		Hash:    hash,
		Enabled: false,
		Updated: time.Now(),
	}
	m.lock.Set(entry)
	if err := m.lock.Save(); err != nil {
		return "", fmt.Errorf("save lockfile: %w", err)
	}
	return def.ID, nil
}

// Remove deletes an installed plugin and updates the lockfile.
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.lock.Get(id); !ok {
		return fmt.Errorf("plugin %s not installed", id)
	}
	targetDir := filepath.Join(m.root, id)
	if err := os.RemoveAll(targetDir); err != nil {
		return fmt.Errorf("remove plugin: %w", err)
	}
	m.lock.Remove(id)
	delete(m.enabled, id)
	return m.lock.Save()
}

// List returns installed plugins from the lockfile.
func (m *Manager) List() []LockEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.entriesLocked()
}

// entriesLocked returns installed plugin entries. Caller must hold m.mu (read).
func (m *Manager) entriesLocked() []LockEntry {
	ids := m.lock.InstalledIDs()
	entries := make([]LockEntry, 0, len(ids))
	for _, id := range ids {
		entry, _ := m.lock.Get(id)
		entries = append(entries, entry)
	}
	return entries
}

// Enable marks a plugin as enabled in the lockfile and runtime map. It first
// checks trust when a trust manager is configured, and re-verifies the
// on-disk content hash so tampered plugins cannot be activated.
func (m *Manager) Enable(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.lock.Get(id)
	if !ok {
		return fmt.Errorf("plugin %s not installed", id)
	}
	if m.trust != nil && !m.trust.IsTrusted(id) {
		return fmt.Errorf("plugin %s is not trusted; use /trust:%s to approve it", id, id)
	}
	if err := m.verifyIntegrityLocked(entry); err != nil {
		return err
	}
	entry.Enabled = true
	entry.Updated = time.Now()
	m.lock.Set(entry)
	m.enabled[id] = true
	return m.lock.Save()
}

// Verify recomputes the content hash of an installed plugin and returns an
// error if it no longer matches the lockfile entry.
func (m *Manager) Verify(id string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.lock.Get(id)
	if !ok {
		return fmt.Errorf("plugin %s not installed", id)
	}
	return m.verifyIntegrityLocked(entry)
}

// verifyIntegrityLocked recomputes the plugin hash and compares it to the
// lockfile entry. Caller must hold m.mu (or m.lock's lock).
func (m *Manager) verifyIntegrityLocked(entry LockEntry) error {
	dir := filepath.Join(m.root, entry.ID)
	current, err := hashPluginDir(dir)
	if err != nil {
		return fmt.Errorf("verify %s: %w", entry.ID, err)
	}
	if current != entry.Hash {
		return fmt.Errorf("plugin %s integrity check failed: content changed since install", entry.ID)
	}
	return nil
}

// Disable marks a plugin as disabled.
func (m *Manager) Disable(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.lock.Get(id)
	if !ok {
		return fmt.Errorf("plugin %s not installed", id)
	}
	entry.Enabled = false
	entry.Updated = time.Now()
	m.lock.Set(entry)
	delete(m.enabled, id)
	return m.lock.Save()
}

// IsEnabled reports whether a plugin is enabled.
func (m *Manager) IsEnabled(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.lock.Get(id)
	return ok && entry.Enabled
}

// EnabledIDs returns the IDs of enabled plugins.
func (m *Manager) EnabledIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var ids []string
	for _, entry := range m.entriesLocked() {
		if entry.Enabled {
			ids = append(ids, entry.ID)
		}
	}
	return ids
}

// EnabledSkillDirs returns the skill directories declared by enabled plugins.
func (m *Manager) EnabledSkillDirs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var dirs []string
	for _, entry := range m.entriesLocked() {
		if !entry.Enabled {
			continue
		}
		def, err := m.loadManifestFor(entry.ID)
		if err != nil {
			continue
		}
		if def.SkillsDir == "" {
			continue
		}
		dir := filepath.Join(m.root, entry.ID, def.SkillsDir)
		if _, err := os.Stat(dir); err == nil {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func (m *Manager) loadManifestFor(id string) (*PluginDef, error) {
	return loadManifest(filepath.Join(m.root, id, "plugin.yaml"))
}

// SetCloneFunc overrides the clone function. Exported for tests.
func (m *Manager) SetCloneFunc(fn func(url, dir string) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cloneFunc = fn
}

// Root returns the plugin installation directory.
func (m *Manager) Root() string { return m.root }

// Lockfile returns the manager's lockfile.
func (m *Manager) Lockfile() *Lockfile { return m.lock }

// validateSource rejects insecure or non-git sources. Plain HTTP is refused
// because plugins execute code and a MITM could serve a malicious payload.
func validateSource(source string) error {
	if !isGitURL(source) {
		return fmt.Errorf("source %q is not a git URL", source)
	}
	if strings.HasPrefix(source, "http://") {
		return fmt.Errorf("refusing insecure http:// plugin source %q; use https:// or git@", source)
	}
	return nil
}

// isGitURL reports whether source looks like a clonable git URL. Accepts
// https://, ssh (git@, ssh://), and an explicit .git suffix over those
// transports. Plain http:// is intentionally not promoted here.
func isGitURL(source string) bool {
	return strings.HasPrefix(source, "https://") ||
		strings.HasPrefix(source, "git@") ||
		strings.HasPrefix(source, "ssh://") ||
		(strings.HasSuffix(source, ".git") &&
			(strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "git@") || strings.HasPrefix(source, "ssh://")))
}

func runGitClone(url, dir string) error {
	cmd := exec.Command("git", "clone", "--depth", "1", url, dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone: %w: %s", err, string(out))
	}
	return nil
}

// moveDir relocates src to dst. It tries an O(1) rename first and falls back
// to a recursive copy + remove so cross-device moves work without shelling
// out to a non-portable `mv`.
func moveDir(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	return copyDir(src, dst)
}

// copyDir recursively copies src to dst preserving file modes, then removes src.
func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat src: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("copyDir: %q is not a directory", src)
	}
	if err := os.MkdirAll(dst, info.Mode()); err != nil {
		return fmt.Errorf("mkdir dst: %w", err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("read src: %w", err)
	}
	for _, entry := range entries {
		s := filepath.Join(src, entry.Name())
		d := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyDir(s, d); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(s, d); err != nil {
			return err
		}
	}
	return os.RemoveAll(src)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy %s: %w", src, err)
	}
	return out.Close()
}
