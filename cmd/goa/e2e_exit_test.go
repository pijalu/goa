//go:build (darwin || linux) && e2e
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package main
import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestGoaE2E_ExitDisablesTerminalProtocols(t *testing.T) {
	binary := buildTestBinary(t)
	defer os.Remove(binary)

	app := startTestApp(t, binary)
	defer app.close()

	app.waitFor(t, "goa", 5*time.Second)
	time.Sleep(300 * time.Millisecond)

	// Exit cleanly via the /quit command so we can inspect the shutdown output.
	app.send(t, "/quit\r")

	done := make(chan struct{})
	go func() {
		app.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		app.pty.Close()
		t.Fatal("process did not exit within 3s of /quit")
	}

	output := app.outputStr()
	if !strings.Contains(output, "\x1b[<u") {
		t.Errorf("shutdown output did not disable Kitty keyboard protocol")
	}
	if !strings.Contains(output, "\x1b[?2004l") {
		t.Errorf("shutdown output did not disable bracketed paste")
	}
	if !strings.Contains(output, "\x1b[>4;0m") {
		t.Errorf("shutdown output did not disable modifyOtherKeys")
	}
	// Regression: TUI.Stop() previously closed its done channel before
	// terminal.Stop() finished, so the process exited mid-restore and these
	// final reset sequences were never written — leaving the parent shell
	// with auto-wrap disabled and no soft reset (corrupted output).
	if !strings.Contains(output, "\x1b[?7h") {
		t.Errorf("shutdown output did not re-enable auto-wrap (DECAWM)")
	}
	if !strings.Contains(output, "\x1b[!p") {
		t.Errorf("shutdown output did not issue a soft terminal reset (DECSTR)")
	}
	if !strings.Contains(output, "\x1b[?12l") {
		t.Errorf("shutdown output did not stop cursor blinking")
	}
}
