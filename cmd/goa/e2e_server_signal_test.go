//go:build (darwin || linux) && e2e

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

// These tests are the measured reproduction for bugs.md B1: `goa server` must be
// stoppable from its OWN console. The web UI session has no TTY (the process
// terminal is replaced by a VirtualTerminal), so Ctrl+C reaches the process as
// SIGINT and nothing read it: the old wiring built a signal.NotifyContext and
// then blocked in a session Run that never observed it, so the signal was
// consumed and the process survived (8 leaked servers, only SIGKILL worked).
//
// Every assertion here is on the real binary over a real PTY: exit within a few
// seconds, exit status 0, the listener closed, and --cpuprofile/--memprofile
// actually flushed.

var serverURLRe = regexp.MustCompile(`goa web UI ready: http://([0-9.]+:[0-9]+)/`)

// startServerApp runs `goa server` on a PTY with an isolated HOME, so the
// console side of the process is exactly what a user has (a terminal in cooked
// mode: the line discipline turns a pressed Ctrl+C into SIGINT).
func startServerApp(t *testing.T, binary string, args ...string) *testApp {
	t.Helper()

	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "HOME="+setupTestHome(t))
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 100, Rows: 30})
	if err != nil {
		t.Fatalf("pty start: %v", err)
	}

	app := &testApp{pty: f, cmd: cmd, done: make(chan struct{})}
	go func() {
		buf := make([]byte, 65536)
		for {
			n, err := f.Read(buf)
			if err != nil {
				close(app.done)
				return
			}
			if n > 0 {
				app.outputMu.Lock()
				app.output.Write(buf[:n])
				app.outputMu.Unlock()
			}
		}
	}()

	// No stray server may outlive the test.
	t.Cleanup(func() {
		if app.cmd.ProcessState == nil {
			_ = app.cmd.Process.Kill()
		}
		app.pty.Close()
		select {
		case <-app.done:
		case <-time.After(2 * time.Second):
		}
	})
	return app
}

// serverAddr waits for the startup line and returns the bound host:port.
func serverAddr(t *testing.T, app *testApp) string {
	t.Helper()
	app.waitFor(t, "goa web UI ready", 20*time.Second)
	m := serverURLRe.FindStringSubmatch(app.outputStr())
	if m == nil {
		t.Fatalf("no bound address in output (%.300s)", app.outputStr())
	}
	return m[1]
}

// exitWatcher starts the process's single Wait and reports the exit status
// once. cmd.Wait may be called only once, so every wait here goes through it.
func exitWatcher(app *testApp) <-chan int {
	code := make(chan int, 1)
	go func() {
		_ = app.cmd.Wait()
		code <- app.cmd.ProcessState.ExitCode()
	}()
	return code
}

// waitForExit waits for the process to end and returns its exit status. It
// fails the test (and kills the process) rather than leak a server.
func waitForExit(t *testing.T, app *testApp, code <-chan int, d time.Duration) int {
	t.Helper()
	select {
	case c := <-code:
		return c
	case <-time.After(d):
		_ = app.cmd.Process.Kill()
		<-code
		t.Fatalf("goa server did not exit within %s (output %.300s)", d, app.outputStr())
		return -1
	}
}

func assertProfileWritten(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Errorf("profile %s was not written: %v", filepath.Base(path), err)
		return
	}
	if info.Size() == 0 {
		t.Errorf("profile %s is empty", filepath.Base(path))
	} else {
		t.Logf("profile %s: %d bytes", filepath.Base(path), info.Size())
	}
}

// assertListenerClosed proves the process released the port, not just that its
// PID disappeared.
func assertListenerClosed(t *testing.T, addr string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err == nil {
		_ = c.Close()
		t.Errorf("listener %s still accepts connections after the process exited", addr)
	}
}

// sendQuitKeys types "/quit" and presses Enter exactly as the page does: one
// key message per keydown, which the server encodes to terminal bytes. (The
// page's raw-input message is its PASTE channel — the editor inserts it
// literally, so it deliberately does not submit.)
func sendQuitKeys(t *testing.T, conn *websocket.Conn) error {
	t.Helper()
	for _, key := range []string{"/", "q", "u", "i", "t", "Enter"} {
		msg := []byte(`{"t":"key","key":{"key":"` + key + `"}}`)
		if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return err
		}
	}
	return nil
}

// Ctrl+C on the server console (a real ^C into the PTY, which the line
// discipline delivers as SIGINT) must stop the process, exit 0, and flush the
// deferred profiles.
func TestGoaE2E_ServerCtrlCStopsProcessWithStatusZero(t *testing.T) {
	binary := buildTestBinary(t)
	defer os.Remove(binary)

	dir := t.TempDir()
	cpu := filepath.Join(dir, "cpu.prof")
	mem := filepath.Join(dir, "mem.prof")

	app := startServerApp(t, binary, "server",
		"--server-addr", "127.0.0.1:0",
		"--cpuprofile", cpu,
		"--memprofile", mem,
	)
	addr := serverAddr(t, app)
	code := exitWatcher(app)

	// Real key press: 0x03 into the console's PTY.
	app.send(t, "\x03")

	if got := waitForExit(t, app, code, 15*time.Second); got != 0 {
		t.Fatalf("exit status after Ctrl+C = %d, want 0 (output %.300s)", got, app.outputStr())
	}
	assertListenerClosed(t, addr)
	assertProfileWritten(t, cpu)
	assertProfileWritten(t, mem)
}

// SIGTERM must behave exactly like Ctrl+C: the same graceful stop, status 0,
// listener released. (The report's repro used `pkill -f` here and every server
// survived it.)
func TestGoaE2E_ServerSigtermStopsProcessWithStatusZero(t *testing.T) {
	binary := buildTestBinary(t)
	defer os.Remove(binary)

	app := startServerApp(t, binary, "server", "--server-addr", "127.0.0.1:0")
	addr := serverAddr(t, app)
	code := exitWatcher(app)

	if err := app.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal SIGTERM: %v", err)
	}

	if got := waitForExit(t, app, code, 15*time.Second); got != 0 {
		t.Fatalf("exit status after SIGTERM = %d, want 0 (output %.300s)", got, app.outputStr())
	}
	assertListenerClosed(t, addr)
}

// `/quit` typed in the BROWSER must end the process just as cleanly: the
// browser drives the same engine as the console, so the stop has to travel the
// same path and take the listener and the profiler flush with it.
func TestGoaE2E_ServerBrowserQuitStopsProcessWithStatusZero(t *testing.T) {
	binary := buildTestBinary(t)
	defer os.Remove(binary)

	app := startServerApp(t, binary, "server", "--server-addr", "127.0.0.1:0")
	addr := serverAddr(t, app)
	// One send at connect time is the deterministic assertion (bugs.md B7): the
	// listener accepts clients the moment it binds, so these bytes arrive while the
	// session is still wiring itself up. They are held by the VirtualTerminal and
	// replayed at engine start, and the submit path is now wired BEFORE that start,
	// so the replayed Enter is acted on. No re-send loop: a gate that only works
	// when the test keeps retrying is not a gate.
	code := exitWatcher(app)

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	if err := sendQuitKeys(t, conn); err != nil {
		t.Fatalf("send /quit: %v", err)
	}

	got := waitForExit(t, app, code, 15*time.Second)
	if got != 0 {
		t.Fatalf("exit status after browser /quit = %d, want 0 (output %.300s)", got, app.outputStr())
	}
	assertListenerClosed(t, addr)
}
