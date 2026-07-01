// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/memory"
	"github.com/pijalu/goa/skills"
)

func TestDreamScheduler_DisabledWhenConfigOff(t *testing.T) {
	subs := &subsystems{cfg: &config.Config{Memory: config.MemoryConfig{Enabled: true}}}
	if s := newDreamScheduler(subs); s != nil {
		t.Fatalf("expected nil scheduler when auto=false")
	}
}

func TestDreamScheduler_Enabled(t *testing.T) {
	dir := t.TempDir()
	subs := &subsystems{
		cfg:      &config.Config{Memory: config.MemoryConfig{Enabled: true, Dream: config.DreamConfig{Enabled: true, Auto: true}}},
		memStore: memory.NewMemoryStore(dir, ""),
	}
	if s := newDreamScheduler(subs); s == nil {
		t.Fatalf("expected scheduler")
	}
}

func TestDreamScheduler_parseInterval(t *testing.T) {
	dir := t.TempDir()
	subs := &subsystems{
		cfg:      &config.Config{Memory: config.MemoryConfig{Dream: config.DreamConfig{Enabled: true, Auto: true}}},
		memStore: memory.NewMemoryStore(dir, ""),
	}
	s := newDreamScheduler(subs)
	if d := s.parseInterval("7d"); d != 7*24*time.Hour {
		t.Fatalf("expected 7d, got %v", d)
	}
	if d := s.parseInterval("1h"); d != time.Hour {
		t.Fatalf("expected 1h, got %v", d)
	}
	if d := s.parseInterval("bad"); d != 0 {
		t.Fatalf("expected 0, got %v", d)
	}
}

func TestDreamScheduler_shouldRunLocked(t *testing.T) {
	dir := t.TempDir()
	subs := &subsystems{
		cfg: &config.Config{Memory: config.MemoryConfig{Dream: config.DreamConfig{
			Enabled:     true,
			Auto:        true,
			Interval:    "1h",
			MinSessions: 2,
		}}},
		memStore: memory.NewMemoryStore(dir, ""),
	}
	s := newDreamScheduler(subs)
	if s.shouldRunLocked() {
		t.Fatalf("should not run without sessions")
	}
	s.sessionsSince = 1
	if s.shouldRunLocked() {
		t.Fatalf("should not run below min sessions")
	}
	s.sessionsSince = 2
	if !s.shouldRunLocked() {
		t.Fatalf("should run with enough sessions and elapsed interval")
	}
	s.lastRun = time.Now().Add(-30 * time.Minute)
	if s.shouldRunLocked() {
		t.Fatalf("should respect interval")
	}
}

func TestDreamScheduler_StartStop(t *testing.T) {
	dir := t.TempDir()
	subs := &subsystems{
		cfg:      &config.Config{Memory: config.MemoryConfig{Dream: config.DreamConfig{Enabled: true, Auto: true, Interval: "1h", MinSessions: 999}}},
		memStore: memory.NewMemoryStore(dir, ""),
	}
	s := newDreamScheduler(subs)
	s.Start()
	time.Sleep(10 * time.Millisecond)
	s.Stop()
}

func TestDreamScheduler_StatePersistence(t *testing.T) {
	dir := t.TempDir()
	subs := &subsystems{
		cfg:        &config.Config{Memory: config.MemoryConfig{Dream: config.DreamConfig{Enabled: true, Auto: true}}},
		memStore:   memory.NewMemoryStore(dir, ""),
		projectDir: dir,
	}
	s := newDreamScheduler(subs)
	s.lastRun = time.Unix(1234567890, 0)
	s.sessionsSince = 5
	if err := s.writeSchedulerState(); err != nil {
		t.Fatalf("write state: %v", err)
	}

	s2 := newDreamScheduler(subs)
	if err := s2.readSchedulerState(); err != nil {
		t.Fatalf("read state: %v", err)
	}
	if s2.lastRun.Unix() != 1234567890 {
		t.Fatalf("unexpected lastRun: %v", s2.lastRun)
	}
	if s2.sessionsSince != 5 {
		t.Fatalf("unexpected sessionsSince: %d", s2.sessionsSince)
	}
}

func TestDreamScheduler_RecordSession(t *testing.T) {
	dir := t.TempDir()
	subs := &subsystems{
		cfg:      &config.Config{Memory: config.MemoryConfig{Dream: config.DreamConfig{Enabled: true, Auto: true}}},
		memStore: memory.NewMemoryStore(dir, ""),
	}
	s := newDreamScheduler(subs)
	s.RecordSession()
	s.RecordSession()
	if s.sessionsSince != 2 {
		t.Fatalf("expected 2 recorded sessions, got %d", s.sessionsSince)
	}
}

func TestDreamScheduler_runDream_NoMemories(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewSkillRegistry(nil)
	reg.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("load skills: %v", err)
	}
	subs := &subsystems{
		cfg:           &config.Config{Memory: config.MemoryConfig{Enabled: true, Dream: config.DreamConfig{Enabled: true, Auto: true}}},
		memStore:      memory.NewMemoryStore(dir, ""),
		projectDir:    dir,
		skillRegistry: reg,
	}
	s := newDreamScheduler(subs)
	if err := s.runDream(context.Background(), 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
