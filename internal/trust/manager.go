// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package trust

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Decision records a trust decision for a domain.
type Decision string

const (
	DecisionTrusted   Decision = "trusted"
	DecisionUntrusted Decision = "untrusted"
	DecisionPrompt    Decision = "prompt"
)

// Manager stores per-project trust decisions.
type Manager struct {
	mu                    sync.RWMutex
	path                  string
	decisions             map[string]Decision
	defaultDecision       Decision
	projectTrustPrompted  bool // whether the user has been asked about project skills
}

// NewManager creates a trust manager.
func NewManager(path string) *Manager {
	m := &Manager{
		path:            path,
		decisions:       make(map[string]Decision),
		defaultDecision: DecisionPrompt,
	}
	_ = m.Load()
	return m
}

// SetDefault sets the default decision for unknown domains.
func (m *Manager) SetDefault(d Decision) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defaultDecision = d
}

// Load reads decisions from disk.
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read trust store: %w", err)
	}
	var store struct {
		Decisions            map[string]Decision `json:"decisions"`
		Default              Decision            `json:"default,omitempty"`
		ProjectTrustPrompted bool                `json:"project_trust_prompted"`
	}
	if err := json.Unmarshal(data, &store); err != nil {
		return fmt.Errorf("parse trust store: %w", err)
	}
	if store.Decisions != nil {
		m.decisions = store.Decisions
	}
	if store.Default != "" {
		m.defaultDecision = store.Default
	}
	m.projectTrustPrompted = store.ProjectTrustPrompted
	return nil
}

// Save persists decisions to disk.
func (m *Manager) Save() error {
	m.mu.RLock()
	store := struct {
		Decisions            map[string]Decision `json:"decisions"`
		Default              Decision            `json:"default,omitempty"`
		ProjectTrustPrompted bool                `json:"project_trust_prompted"`
	}{
		Decisions:            m.decisions,
		Default:              m.defaultDecision,
		ProjectTrustPrompted: m.projectTrustPrompted,
	}
	m.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return fmt.Errorf("mkdir trust store: %w", err)
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.path, data, 0o644)
}

// IsTrusted reports whether domain is trusted.
func (m *Manager) IsTrusted(domain string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.decisions[domain]
	if !ok {
		return m.defaultDecision == DecisionTrusted
	}
	return d == DecisionTrusted
}

// Decision returns the stored decision for a domain.
func (m *Manager) Decision(domain string) Decision {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if d, ok := m.decisions[domain]; ok {
		return d
	}
	return m.defaultDecision
}

// Trust marks domain as trusted.
func (m *Manager) Trust(domain string) error {
	m.mu.Lock()
	m.decisions[domain] = DecisionTrusted
	m.mu.Unlock()
	return m.Save()
}

// Untrust marks domain as untrusted.
func (m *Manager) Untrust(domain string) error {
	m.mu.Lock()
	m.decisions[domain] = DecisionUntrusted
	m.mu.Unlock()
	return m.Save()
}

// Prompt marks domain as prompt.
func (m *Manager) Prompt(domain string) error {
	m.mu.Lock()
	m.decisions[domain] = DecisionPrompt
	m.mu.Unlock()
	return m.Save()
}

// Domains returns all known domains.
func (m *Manager) Domains() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.decisions))
	for d := range m.decisions {
		out = append(out, d)
	}
	return out
}

// NeedProjectTrustPrompt returns true when the user has never been asked
// whether to trust this project's skills and the current default is prompt.
func (m *Manager) NeedProjectTrustPrompt() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return !m.projectTrustPrompted && m.defaultDecision == DecisionPrompt
}

// SetProjectTrustPrompted records that the user has been asked and optionally
// sets the default decision for unknown domains. When trusted is true the
// default becomes DecisionTrusted; when false it stays DecisionPrompt but the
// prompt flag is set so we never ask again. The result is persisted.
func (m *Manager) SetProjectTrustPrompted(trusted bool) error {
	m.mu.Lock()
	if trusted {
		m.defaultDecision = DecisionTrusted
	}
	m.projectTrustPrompted = true
	m.mu.Unlock()
	return m.Save()
}
