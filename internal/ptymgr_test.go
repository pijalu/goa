//go:build darwin || linux
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal
import (
	"strings"
	"testing"
	"time"
)

func TestNewPTYManager_CreatesEmpty(t *testing.T) {
	mgr := NewPTYManager()
	if mgr == nil {
		t.Fatal("NewPTYManager should return non-nil")
	}
	sessions := mgr.List()
	if len(sessions) != 0 {
		t.Errorf("New manager should have 0 sessions, got %d", len(sessions))
	}
}

func TestPTYManager_Start_StartsCommand(t *testing.T) {
	mgr := NewPTYManager()
	defer mgr.Cleanup()

	session, err := mgr.Start("test1", "echo hello", 80, 24)
	if err != nil {
		t.Fatalf("Start should succeed: %v", err)
	}
	if session == nil {
		t.Fatal("Start should return non-nil session")
	}
	if session.ID != "test1" {
		t.Errorf("session.ID = %q, want %q", session.ID, "test1")
	}
	if !session.running {
		t.Error("session should be running after Start")
	}
	// Give output time to arrive
	time.Sleep(100 * time.Millisecond)
	if session.Buffer.Len() == 0 {
		t.Error("session should have output after command runs")
	}
}

func TestPTYManager_Start_WritesOutput(t *testing.T) {
	mgr := NewPTYManager()
	defer mgr.Cleanup()

	_, err := mgr.Start("echo-test", "echo HELLO_PTY", 80, 24)
	if err != nil {
		t.Fatalf("Start should succeed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	output, err := mgr.Read("echo-test", 0)
	if err != nil {
		t.Fatalf("Read should succeed: %v", err)
	}
	if !strings.Contains(output, "HELLO_PTY") {
		t.Errorf("Expected output to contain HELLO_PTY, got: %q", output)
	}
}

func TestPTYManager_Start_MultipleSessions(t *testing.T) {
	mgr := NewPTYManager()
	defer mgr.Cleanup()

	s1, err := mgr.Start("s1", "echo one", 80, 24)
	if err != nil {
		t.Fatalf("Start s1: %v", err)
	}
	s2, err := mgr.Start("s2", "echo two", 80, 24)
	if err != nil {
		t.Fatalf("Start s2: %v", err)
	}

	if s1.ID == s2.ID {
		t.Error("Sessions should have different IDs")
	}

	sessions := mgr.List()
	if len(sessions) != 2 {
		t.Errorf("Expected 2 sessions, got %d", len(sessions))
	}
}

func TestPTYManager_Start_DuplicateID_ReturnsError(t *testing.T) {
	mgr := NewPTYManager()
	defer mgr.Cleanup()

	_, err := mgr.Start("dup", "echo test", 80, 24)
	if err != nil {
		t.Fatalf("First start: %v", err)
	}

	_, err = mgr.Start("dup", "echo test2", 80, 24)
	if err == nil {
		t.Error("Duplicate session ID should return error")
	}
}

func TestPTYManager_Write_SendsInput(t *testing.T) {
	mgr := NewPTYManager()
	defer mgr.Cleanup()

	_, err := mgr.Start("write-test", "cat", 80, 24)
	if err != nil {
		t.Fatalf("Start should succeed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	err = mgr.Write("write-test", "hello from pty\n")
	if err != nil {
		t.Fatalf("Write should succeed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	err = mgr.Write("write-test", "\x04") // Ctrl+D to end cat
	if err != nil {
		t.Fatalf("Write Ctrl+D should succeed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	output, err := mgr.Read("write-test", 0)
	if err != nil {
		t.Fatalf("Read should succeed: %v", err)
	}
	if !strings.Contains(output, "hello from pty") {
		t.Errorf("Expected output to contain 'hello from pty', got: %q", output)
	}
}

func TestPTYManager_ReadBlocking_ReturnsOutput(t *testing.T) {
	mgr := NewPTYManager()
	defer mgr.Cleanup()

	_, err := mgr.Start("blocking-test", "echo blocking_read_output", 80, 24)
	if err != nil {
		t.Fatalf("Start should succeed: %v", err)
	}

	output, err := mgr.ReadBlocking("blocking-test", 3*time.Second)
	if err != nil {
		t.Fatalf("ReadBlocking should succeed: %v", err)
	}
	if !strings.Contains(output, "blocking_read_output") {
		t.Errorf("Expected output to contain 'blocking_read_output', got: %q", output)
	}
}

func TestPTYManager_Resize_ChangesDimensions(t *testing.T) {
	mgr := NewPTYManager()
	defer mgr.Cleanup()

	_, err := mgr.Start("resize-test", "echo resize_works", 80, 24)
	if err != nil {
		t.Fatalf("Start should succeed: %v", err)
	}

	err = mgr.Resize("resize-test", 120, 40)
	if err != nil {
		t.Fatalf("Resize should succeed: %v", err)
	}
}

func TestPTYManager_Stop_TerminatesSession(t *testing.T) {
	mgr := NewPTYManager()
	defer mgr.Cleanup()

	_, err := mgr.Start("stop-test", "sleep 60", 80, 24)
	if err != nil {
		t.Fatalf("Start should succeed: %v", err)
	}

	err = mgr.Stop("stop-test")
	if err != nil {
		t.Fatalf("Stop should succeed: %v", err)
	}

	// Should no longer be listed
	sessions := mgr.List()
	for _, s := range sessions {
		if s.ID == "stop-test" {
			t.Error("Stopped session should not be in list")
		}
	}
}

func TestPTYManager_Stop_Nonexistent_ReturnsError(t *testing.T) {
	mgr := NewPTYManager()
	err := mgr.Stop("nonexistent")
	if err == nil {
		t.Error("Stopping nonexistent session should return error")
	}
}

func TestPTYManager_Cleanup_RemovesAll(t *testing.T) {
	mgr := NewPTYManager()

	mgr.Start("c1", "sleep 10", 80, 24)
	mgr.Start("c2", "sleep 10", 80, 24)

	mgr.Cleanup()

	sessions := mgr.List()
	if len(sessions) != 0 {
		t.Errorf("After cleanup, expected 0 sessions, got %d", len(sessions))
	}
}

func TestPTYManager_List_ShowsSessionDetails(t *testing.T) {
	mgr := NewPTYManager()
	defer mgr.Cleanup()

	mgr.Start("list-test", "echo data", 80, 24)
	time.Sleep(50 * time.Millisecond)

	sessions := mgr.List()
	found := false
	for _, s := range sessions {
		if s.ID == "list-test" {
			found = true
			if !s.Running {
				t.Error("Session should be marked as running")
			}
			if s.PID <= 0 {
				t.Errorf("Session should have valid PID, got %d", s.PID)
			}
			if s.Command == "" {
				t.Error("Session should have command set")
			}
		}
	}
	if !found {
		t.Error("Session list-test not found in listing")
	}
}

func TestPTYManager_Write_ToStoppedSession_ReturnsError(t *testing.T) {
	mgr := NewPTYManager()
	defer mgr.Cleanup()

	mgr.Start("wstop", "echo test", 80, 24)
	mgr.Stop("wstop")

	err := mgr.Write("wstop", "data")
	if err == nil {
		t.Error("Write to stopped session should return error")
	}
}

func TestPTYManager_Read_Nonexistent_ReturnsError(t *testing.T) {
	mgr := NewPTYManager()
	_, err := mgr.Read("no-such-session", 10)
	if err == nil {
		t.Error("Read from nonexistent session should return error")
	}
}

func TestPTYManager_Resize_Nonexistent_ReturnsError(t *testing.T) {
	mgr := NewPTYManager()
	err := mgr.Resize("no-such-session", 80, 24)
	if err == nil {
		t.Error("Resize nonexistent session should return error")
	}
}

func TestPTYManager_Start_LargeOutput_ReadsAll(t *testing.T) {
	mgr := NewPTYManager()
	defer mgr.Cleanup()

	// Generate 100 lines of output
	cmd := "for i in $(seq 1 100); do echo 'line'$i; done"
	_, err := mgr.Start("large", cmd, 80, 24)
	if err != nil {
		t.Fatalf("Start should succeed: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	output, err := mgr.Read("large", 0)
	if err != nil {
		t.Fatalf("Read should succeed: %v", err)
	}
	lines := strings.Count(output, "line")
	if lines < 90 {
		t.Errorf("Expected at least 90 lines, got %d. Output length: %d", lines, len(output))
	}
}
