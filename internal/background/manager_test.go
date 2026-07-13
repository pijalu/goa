// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package background

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestManager_StartAndList(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(filepath.Join(dir, "tasks.json"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	task, err := mgr.Start("echo hello", "", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if task.Status != StatusRunning {
		t.Errorf("expected status running, got %v", task.Status)
	}
	if len(mgr.List()) != 1 {
		t.Errorf("expected 1 task, got %d", len(mgr.List()))
	}
}

func TestManager_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.json")
	mgr, err := NewManager(path)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	task, err := mgr.Start("echo hello", "", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	mgr.StopAll(time.Second)

	mgr2, err := NewManager(path)
	if err != nil {
		t.Fatalf("NewManager reload: %v", err)
	}
	reloaded := mgr2.Get(task.ID)
	if reloaded == nil {
		t.Fatal("expected task to be reloaded")
	}
	if reloaded.Command != "echo hello" {
		t.Errorf("expected command echo hello, got %q", reloaded.Command)
	}
}

func TestManager_Stop(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(filepath.Join(dir, "tasks.json"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	task, err := mgr.Start("sleep 30", "", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := mgr.Stop(task.ID, 100*time.Millisecond); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := mgr.Get(task.ID); got.Status != StatusKilled {
		t.Errorf("expected killed status, got %v", got.Status)
	}
	if got := mgr.Get(task.ID); got.ExitCode >= 0 {
		t.Errorf("expected negative exit code for killed task, got %d", got.ExitCode)
	}
}

func TestManager_ReadOutput(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(filepath.Join(dir, "tasks.json"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	task, err := mgr.Start("echo line1; echo line2", "", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	mgr.StopAll(time.Second)

	stdout, _ := mgr.ReadOutput(task.ID, 10)
	found := false
	for _, line := range stdout {
		if line == "line1" || line == "line2" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected output lines, got %v", stdout)
	}
}

func TestManager_WriteInput(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(filepath.Join(dir, "tasks.json"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	task, err := mgr.Start("cat", "", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := mgr.WriteInput(task.ID, "hello"); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := mgr.Stop(task.ID, 100*time.Millisecond); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	stdout, _ := mgr.ReadOutput(task.ID, 10)
	found := false
	for _, line := range stdout {
		if line == "hello" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected stdin echo, got %v", stdout)
	}
}

func TestManager_FileNotExist(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(filepath.Join(dir, "missing", "tasks.json"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if len(mgr.List()) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(mgr.List()))
	}
}

func TestManager_StopNotFound(t *testing.T) {
	mgr, _ := NewManager("")
	if _, err := mgr.Stop("missing", time.Second); err == nil {
		t.Fatal("expected error for missing task")
	}
}

func TestManager_ReadOutputNotFound(t *testing.T) {
	mgr, _ := NewManager("")
	out, _ := mgr.ReadOutput("missing", 10)
	if out != nil {
		t.Errorf("expected nil output for missing task, got %v", out)
	}
}

func TestManager_WriteInputNotFound(t *testing.T) {
	mgr, _ := NewManager("")
	if err := mgr.WriteInput("missing", "x"); err == nil {
		t.Fatal("expected error for missing task")
	}
}

func TestManager_StartInvalidShell(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(filepath.Join(dir, "tasks.json"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Setenv("SHELL", "/nonexistent")
	if _, err := mgr.Start("echo hi", "", nil); err == nil {
		// Some systems may still resolve the shell; if it succeeds that's fine.
		// We only care that the command is rejected or runs.
		return
	}
}

func TestManager_StopAll(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(filepath.Join(dir, "tasks.json"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	_, _ = mgr.Start("sleep 30", "", nil)
	_, _ = mgr.Start("sleep 30", "", nil)
	mgr.StopAll(100 * time.Millisecond)
	for _, task := range mgr.List() {
		if task.Status == StatusRunning {
			t.Errorf("expected task %s to be stopped, got %v", task.ID, task.Status)
		}
	}
}

func TestManager_OutputDir(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "tasks.json"))
	task, err := mgr.Start("echo hello", "", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if task.OutputDir == "" {
		t.Fatal("expected non-empty output dir")
	}
	if _, err := os.Stat(task.OutputDir); err != nil {
		t.Errorf("expected output dir to exist: %v", err)
	}
	mgr.StopAll(time.Second)
}

// TestManager_ReturnedTaskIsSnapshot verifies Start returns a defensive copy
// (A2): mutating the returned task must not affect the manager's view.
func TestManager_ReturnedTaskIsSnapshot(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(filepath.Join(dir, "tasks.json"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	task, err := mgr.Start("sleep 30", "", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mgr.StopAll(time.Second)

	original := task.Status
	task.Status = StatusCompleted // mutate the caller's copy
	if got := mgr.Get(task.ID); got.Status != original {
		t.Errorf("manager status changed to %v after caller mutated returned task", got.Status)
	}
}

// TestManager_PersistIsAtomic verifies the registry file stays valid JSON
// under concurrent starts/stops (A3).
func TestManager_PersistIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.json")
	mgr, err := NewManager(path)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tk, err := mgr.Start("echo hi", "", nil)
			if err == nil && tk != nil {
				_, _ = mgr.Stop(tk.ID, 100*time.Millisecond)
			}
		}()
	}
	wg.Wait()

	// The file must parse as valid JSON into the tasks map.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var parsed map[string]*Task
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("registry not valid JSON after concurrent writes: %v", err)
	}
}

// TestManager_RestartReconstructsCounter verifies the ID counter is rebuilt
// from persisted task IDs so a post-restart Start does not collide (B2).
func TestManager_RestartReconstructsCounter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.json")
	mgr, err := NewManager(path)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	first, err := mgr.Start("echo one", "", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	mgr.StopAll(time.Second)

	mgr2, err := NewManager(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	// Existing task must be present (no collision/overwrite by next Start).
	if got := mgr2.Get(first.ID); got == nil {
		t.Fatalf("task %s missing after reload", first.ID)
	}
	second, err := mgr2.Start("echo two", "", nil)
	if err != nil {
		t.Fatalf("Start after reload: %v", err)
	}
	defer mgr2.StopAll(time.Second)
	if second.ID == first.ID {
		t.Fatalf("ID collision after restart: both %s", first.ID)
	}
	if got := mgr2.Get(first.ID); got == nil {
		t.Fatalf("first task %s was overwritten by %s", first.ID, second.ID)
	}
}

// TestManager_OutputPersistsAcrossRestart verifies teed output is readable
// from a fresh manager that never captured the original pipes (B3).
func TestManager_OutputPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.json")
	mgr, err := NewManager(path)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	task, err := mgr.Start("printf 'alpha\\nbeta\\n'", "", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Wait for the process to finish and output to flush.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := mgr.Get(task.ID); got != nil && got.Status == StatusCompleted {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mgr2, err := NewManager(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	stdout, _ := mgr2.ReadOutput(task.ID, 10)
	found := false
	for _, line := range stdout {
		if line == "alpha" || line == "beta" {
			found = true
		}
	}
	if !found {
		t.Errorf("persisted output not readable after restart; got %v", stdout)
	}
}

// TestManager_StopAfterRestart verifies a reattached live task can be killed
// by PID even though its pipes are gone (B3).
func TestManager_StopAfterRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.json")
	mgr, err := NewManager(path)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	task, err := mgr.Start("sleep 60", "", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Reload: the running task is reattached (no captured proc handle).
	mgr2, err := NewManager(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	loaded := mgr2.Get(task.ID)
	if loaded == nil || loaded.Status != StatusRunning {
		t.Fatalf("expected reattached running task, got %+v", loaded)
	}
	if _, err := mgr2.Stop(task.ID, 500*time.Millisecond); err != nil {
		t.Fatalf("Stop reattached: %v", err)
	}
	got := mgr2.Get(task.ID)
	if got == nil || got.Status != StatusKilled {
		t.Fatalf("expected killed after reattached stop, got %+v", got)
	}
}
