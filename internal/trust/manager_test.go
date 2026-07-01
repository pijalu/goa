// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package trust

import (
	"path/filepath"
	"testing"
)

func TestTrustManagerLifecycle(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(filepath.Join(dir, "trust.json"))
	if m.IsTrusted("plugins") {
		t.Error("new manager should not trust unknown domain")
	}
	if err := m.Trust("plugins"); err != nil {
		t.Fatalf("trust: %v", err)
	}
	if !m.IsTrusted("plugins") {
		t.Error("expected trusted")
	}
	if err := m.Untrust("skills"); err != nil {
		t.Fatalf("untrust: %v", err)
	}
	if m.IsTrusted("skills") {
		t.Error("expected untrusted")
	}
}

func TestTrustManagerDefault(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(filepath.Join(dir, "trust.json"))
	m.SetDefault(DecisionTrusted)
	if !m.IsTrusted("unknown") {
		t.Error("expected default trusted")
	}
}

func TestTrustManagerPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")
	m := NewManager(path)
	_ = m.Trust("x")

	m2 := NewManager(path)
	if !m2.IsTrusted("x") {
		t.Error("expected trust persisted")
	}
}

func TestNeedProjectTrustPrompt_FreshManager(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(filepath.Join(dir, "trust.json"))
	if !m.NeedProjectTrustPrompt() {
		t.Error("fresh manager should need project trust prompt")
	}
}

func TestNeedProjectTrustPrompt_AfterSetTrusted(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(filepath.Join(dir, "trust.json"))
	if err := m.SetProjectTrustPrompted(true); err != nil {
		t.Fatalf("SetProjectTrustPrompted(true): %v", err)
	}
	if m.NeedProjectTrustPrompt() {
		t.Error("after SetProjectTrustPrompted(true), should not need prompt")
	}
	if !m.IsTrusted("anything") {
		t.Error("after SetProjectTrustPrompted(true), default should be trusted")
	}
}

func TestNeedProjectTrustPrompt_AfterSetNotTrusted(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(filepath.Join(dir, "trust.json"))
	if err := m.SetProjectTrustPrompted(false); err != nil {
		t.Fatalf("SetProjectTrustPrompted(false): %v", err)
	}
	if m.NeedProjectTrustPrompt() {
		t.Error("after SetProjectTrustPrompted(false), should not need prompt")
	}
	// Default should remain DecisionPrompt (not trusted by default)
	if m.IsTrusted("anything") {
		t.Error("after SetProjectTrustPrompted(false), default should still be prompt")
	}
}

func TestNeedProjectTrustPrompt_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	m := NewManager(path)
	_ = m.SetProjectTrustPrompted(true)

	m2 := NewManager(path)
	if m2.NeedProjectTrustPrompt() {
		t.Error("project trust prompt decision should persist across reloads")
	}
	if !m2.IsTrusted("anything") {
		t.Error("default trust should persist across reloads")
	}
}
