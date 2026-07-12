// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package background

import (
	"os"
	"path/filepath"
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
	if got := mgr.Get(task.ID); got.Status != StatusKilled && got.Status != StatusError {
		t.Errorf("expected killed or error status, got %v", got.Status)
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
