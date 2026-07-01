// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"testing"
)

func TestThemeStoreDefault(t *testing.T) {
	s := NewThemeStore(t.TempDir())
	if s.Active().Name != "default" {
		t.Errorf("active = %s", s.Active().Name)
	}
}

func TestThemeStoreSaveLoad(t *testing.T) {
	dir := t.TempDir()
	s := NewThemeStore(dir)
	theme := &Theme{Name: "dark", Colors: map[string]string{"primary": "blue"}}
	if err := s.Save(theme); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := s.Load("dark")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Colors["primary"] != "blue" {
		t.Errorf("color = %s", loaded.Colors["primary"])
	}
	names, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, n := range names {
		if n == "dark" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected dark in %v", names)
	}
}

func TestThemeStorePersistence(t *testing.T) {
	dir := t.TempDir()
	s := NewThemeStore(dir)
	_ = s.Save(&Theme{Name: "light"})

	s2 := NewThemeStore(dir)
	names, _ := s2.List()
	found := false
	for _, n := range names {
		if n == "light" {
			found = true
			break
		}
	}
	if !found {
		t.Error("theme not persisted")
	}
}
