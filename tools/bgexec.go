// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"bufio"
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
)

// BGProcess represents a running background process.
type BGProcess struct {
	ID        string
	Command   string
	PID       int
	StartTime time.Time
	Cmd       *exec.Cmd
	Stdout    *ringBuffer
	Stderr    *ringBuffer
	Stdin     io.WriteCloser
	done      chan struct{}
	exitCode  atomic.Int32 // valid after done is closed
	scannerWg sync.WaitGroup // tracks output scanner goroutines
}

// ringBuffer is a circular buffer for process output.
type ringBuffer struct {
	mu    sync.Mutex
	buf   []string
	size  int
	pos   int
	count int
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{
		buf:  make([]string, size),
		size: size,
	}
}

func (rb *ringBuffer) Write(line string) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.buf[rb.pos] = line
	rb.pos = (rb.pos + 1) % rb.size
	if rb.count < rb.size {
		rb.count++
	}
}

func (rb *ringBuffer) ReadLast(n int) []string {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if n > rb.count {
		n = rb.count
	}
	result := make([]string, n)
	for i := 0; i < n; i++ {
		idx := (rb.pos - n + i) % rb.size
		if idx < 0 {
			idx += rb.size
		}
		result[i] = rb.buf[idx]
	}
	return result
}

// BGExecTool manages background processes with pipe I/O and ring buffer output.
type BGExecTool struct {
	mu      sync.RWMutex
	procs   map[string]*BGProcess
	counter int
}

// NewBGExecTool creates a new BGExecTool.
func NewBGExecTool() *BGExecTool {
	t := &BGExecTool{
		procs: make(map[string]*BGProcess),
	}
	// Register cleanup on exit
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-c
		t.StopAll()
	}()
	return t
}

// Schema returns the tool schema for bg_exec.
func (t *BGExecTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "bg_exec",
		Description: "Manage long-running background processes with pipe I/O.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string", "description": "Action to perform",
					"enum": []string{"start", "status", "read", "write", "stop", "list"},
				},
				"command": map[string]any{
					"type": "string", "description": "Command to start",
				},
				"id": map[string]any{
					"type": "string", "description": "Process ID for status/read/write/stop actions",
				},
				"workdir": map[string]any{
					"type": "string", "description": "Working directory",
				},
				"env": map[string]any{
					"type": "object", "additionalProperties": map[string]any{"type": "string"},
					"description": "Environment variables",
				},
				"input": map[string]any{
					"type": "string", "description": "Input to write to process stdin",
				},
				"tail_lines": map[string]any{
					"type": "integer", "description": "Number of recent lines to read (default: 20)",
				},
			},
			"required": []string{"action"},
		},
	}
}

// bgExecCommon holds fields common across all bg_exec actions.
type bgExecParams struct {
	Action    string            `json:"action"`
	ID        string            `json:"id"`
	Command   string            `json:"command"`
	Workdir   string            `json:"workdir"`
	Env       map[string]string `json:"env"`
	Input     string            `json:"input"`
	TailLines int               `json:"tail_lines"`
}

// Execute runs the requested background process action.
func (t *BGExecTool) Execute(input string) (string, error) {
	var p bgExecParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", &internal.ToolError{
			Tool: "bg_exec", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot parse parameters: %v", err),
			HintText: "Ensure your input is valid JSON with the required fields.",
		}
	}
	switch p.Action {
	case "start":
		return t.start(p)
	case "status":
		return t.status(p)
	case "read":
		return t.read(p)
	case "write":
		return t.write(p)
	case "stop":
		return t.stop(p)
	case "list":
		return t.list()
	default:
		return "", &internal.ToolError{
			Tool: "bg_exec", Type: "unknown_action",
			Detail:   fmt.Sprintf("Unknown action: %s", p.Action),
			HintText: "Use one of: start, status, read, write, stop, list",
		}
	}
}

func (t *BGExecTool) IsRetryable(err error) bool { return false }

//go:embed bgexec.short.md bgexec.long.md
var bgexecDocs embed.FS

func (t *BGExecTool) ShortDoc() string { return readDoc(bgexecDocs, "bgexec.short.md") }
func (t *BGExecTool) LongDoc() string  { return readDoc(bgexecDocs, "bgexec.long.md") }

func (t *BGExecTool) Examples() []string {
	return []string{
		`{"action": "start", "command": "npm run dev"}`,
		`{"action": "status", "id": "proc-1"}`,
		`{"action": "read", "id": "proc-1", "tail_lines": 50}`,
		`{"action": "stop", "id": "proc-1"}`,
	}
}

func (t *BGExecTool) start(p bgExecParams) (string, error) {
	if p.Command == "" {
		return "", &internal.ToolError{
			Tool: "bg_exec", Type: "missing_command",
			Detail: "No command provided", HintText: "Provide a command in the 'command' field.",
		}
	}

	t.mu.Lock()
	t.counter++
	id := fmt.Sprintf("proc-%d", t.counter)
	t.mu.Unlock()

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	cmd := exec.Command(shell, "-c", p.Command)

	if p.Workdir != "" {
		cmd.Dir = p.Workdir
	}

	// Set up pipes
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", bgErr("pipe_error", fmt.Sprintf("stdout pipe: %v", err))
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", bgErr("pipe_error", fmt.Sprintf("stderr pipe: %v", err))
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", bgErr("pipe_error", fmt.Sprintf("stdin pipe: %v", err))
	}

	if err := cmd.Start(); err != nil {
		return "", bgErr("start_failed", err.Error())
	}

	proc := &BGProcess{
		ID:        id,
		Command:   p.Command,
		PID:       cmd.Process.Pid,
		StartTime: time.Now(),
		Cmd:       cmd,
		Stdout:    newRingBuffer(10000),
		Stderr:    newRingBuffer(10000),
		Stdin:     stdin,
		done:      make(chan struct{}),
	}

	// Start output readers
	proc.scannerWg.Add(2)
	go func() {
		defer proc.scannerWg.Done()
		t.readOutput(proc, stdout, proc.Stdout)
	}()
	go func() {
		defer proc.scannerWg.Done()
		t.readOutput(proc, stderr, proc.Stderr)
	}()

	// Wait in background
	go func() {
		proc.Cmd.Wait()
		proc.exitCode.Store(int32(proc.Cmd.ProcessState.ExitCode()))
		// Wait for scanner goroutines to finish writing to ring buffers before
		// signalling done. cmd.Wait() closes the pipes but does not wait for
		// pipe readers to complete, so without this synchronisation a reader of
		// the ring buffer (e.g. a test waiting on done) may observe the last
		// line(s) missing.
		proc.scannerWg.Wait()
		close(proc.done)
	}()

	t.mu.Lock()
	t.procs[id] = proc
	t.mu.Unlock()

	return fmt.Sprintf("[bg_exec: start] Process %s started — PID %d\nCommand: %s", id, proc.PID, p.Command), nil
}

// maxScanLineLen is the largest single line readOutput will buffer.
// The default bufio.Scanner token size is 64 KiB, which silently corrupts
// long lines (e.g. minified bundles). 1 MiB covers those without unbounded
// memory growth.
const maxScanLineLen = 1 << 20

// readScanBufSize is the initial buffer size for the scanner used in
// readOutput. A zero-length initial buffer (< 1.21) was known to confuse
// the scanner's growth logic on some platforms, causing lines near the
// buffer boundary to be silently truncated. Using a full-size initial
// buffer ensures the first read fills it completely before any grow
// decision is made.
const readScanBufSize = 64 * 1024

func (t *BGExecTool) readOutput(proc *BGProcess, reader io.ReadCloser, buf *ringBuffer) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, readScanBufSize), maxScanLineLen)
	for scanner.Scan() {
		buf.Write(scanner.Text())
	}
	// Surface scanner failures (e.g. a line exceeding maxScanLineLen) instead
	// of dropping them silently. There is no logger here, so record a marker
	// so consumers know output may be truncated.
	if err := scanner.Err(); err != nil {
		buf.Write(fmt.Sprintf("[bg_exec: read error: %v]", err))
	}
}

func (t *BGExecTool) status(p bgExecParams) (string, error) {
	id := p.ID
	if id == "" {
		return "", bgErr("missing_id", "id is required for status action")
	}

	t.mu.RLock()
	proc, ok := t.procs[id]
	t.mu.RUnlock()

	if !ok {
		return "", &internal.ToolError{
			Tool: "bg_exec", Type: "process_not_found",
			Detail:   fmt.Sprintf("Process %q not found", id),
			HintText: "Use list action to see active processes.",
		}
	}

	select {
	case <-proc.done:
		return fmt.Sprintf("[bg_exec: status] %s — exited with code %d (ran for %s)",
			id, proc.exitCode.Load(), time.Since(proc.StartTime).Round(time.Second)), nil
	default:
		return fmt.Sprintf("[bg_exec: status] %s — running (PID %d, uptime %s)",
			id, proc.PID, time.Since(proc.StartTime).Round(time.Second)), nil
	}
}

func (t *BGExecTool) read(p bgExecParams) (string, error) {
	id := p.ID
	tailLines := p.TailLines
	if tailLines <= 0 {
		tailLines = 20
	}

	if id == "" {
		return "", bgErr("missing_id", "id is required for read action")
	}

	t.mu.RLock()
	proc, ok := t.procs[id]
	t.mu.RUnlock()

	if !ok {
		return "", &internal.ToolError{
			Tool: "bg_exec", Type: "process_not_found",
			Detail:   fmt.Sprintf("Process %q not found", id),
			HintText: "Use list action to see active processes.",
		}
	}

	stdoutLines := proc.Stdout.ReadLast(tailLines)
	stderrLines := proc.Stderr.ReadLast(tailLines)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "[bg_exec: read] %s — last %d lines\n", id, tailLines)
	fmt.Fprintf(&buf, "--- stdout ---\n")
	for _, line := range stdoutLines {
		fmt.Fprintln(&buf, line)
	}
	fmt.Fprintf(&buf, "--- stderr ---\n")
	for _, line := range stderrLines {
		fmt.Fprintln(&buf, line)
	}
	return buf.String(), nil
}

func (t *BGExecTool) write(p bgExecParams) (string, error) {
	id := p.ID
	text := p.Input

	if id == "" {
		return "", bgErr("missing_id", "id is required for write action")
	}

	t.mu.RLock()
	proc, ok := t.procs[id]
	t.mu.RUnlock()

	if !ok {
		return "", &internal.ToolError{
			Tool: "bg_exec", Type: "process_not_found",
			Detail:   fmt.Sprintf("Process %q not found", id),
			HintText: "Use list action to see active processes.",
		}
	}

	if _, err := io.WriteString(proc.Stdin, text+"\n"); err != nil {
		return "", bgErr("write_failed", fmt.Sprintf("write to stdin: %v", err))
	}
	return fmt.Sprintf("[bg_exec: write] %s — wrote %d bytes to stdin", id, len(text)), nil
}

func (t *BGExecTool) stop(p bgExecParams) (string, error) {
	id := p.ID
	if id == "" {
		return "", bgErr("missing_id", "id is required for stop action")
	}

	t.mu.Lock()
	proc, ok := t.procs[id]
	delete(t.procs, id)
	t.mu.Unlock()

	if !ok {
		return "", bgErr("process_not_found", fmt.Sprintf("Process %q not found", id))
	}

	return terminateProc(proc, id)
}

// sigkillGrace is the time terminateProc waits after SIGTERM before SIGKILL.
const sigkillGrace = 5 * time.Second

// terminateProc sends SIGTERM then SIGKILL (after a grace period) to a process
// and reports the outcome. It is safe to call on a process that has already
// exited. Signal errors are surfaced instead of being discarded.
func terminateProc(proc *BGProcess, id string) (string, error) {
	select {
	case <-proc.done:
		return fmt.Sprintf("[bg_exec: stop] %s — already exited (code %d)", id, proc.exitCode.Load()), nil
	default:
	}
	if err := proc.Cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return "", bgErr("signal_failed", fmt.Sprintf("SIGTERM %s: %v", id, err))
	}
	select {
	case <-proc.done:
		return fmt.Sprintf("[bg_exec: stop] %s — terminated (SIGTERM)", id), nil
	case <-time.After(sigkillGrace):
	}
	if err := proc.Cmd.Process.Kill(); err != nil {
		return "", bgErr("signal_failed", fmt.Sprintf("SIGKILL %s: %v", id, err))
	}
	<-proc.done
	return fmt.Sprintf("[bg_exec: stop] %s — force killed (SIGKILL)", id), nil
}

func (t *BGExecTool) list() (string, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if len(t.procs) == 0 {
		return "[bg_exec: list] No active processes", nil
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "[bg_exec: list] %d active process(es)\n", len(t.procs))
	for id, proc := range t.procs {
		select {
		case <-proc.done:
			fmt.Fprintf(&buf, "  %s — exited (%d), ran %s\n", id, proc.exitCode.Load(), time.Since(proc.StartTime).Round(time.Second))
		default:
			fmt.Fprintf(&buf, "  %s — PID %d, running %s\n", id, proc.PID, time.Since(proc.StartTime).Round(time.Second))
		}
	}
	return buf.String(), nil
}

// StopAll terminates all running background processes.
func (t *BGExecTool) StopAll() {
	t.mu.Lock()
	defer t.mu.Unlock()

	for id, proc := range t.procs {
		stopProcGraceful(proc, 3*time.Second)
		delete(t.procs, id)
	}
}

// stopProcGraceful signals a process (skipping if already exited) and waits
// up to grace for it to terminate before escalating to SIGKILL.
func stopProcGraceful(proc *BGProcess, grace time.Duration) {
	select {
	case <-proc.done:
		return
	default:
	}
	_ = proc.Cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-proc.done:
	case <-time.After(grace):
		_ = proc.Cmd.Process.Kill()
	}
}

// bgErr builds a *internal.ToolError for bg_exec actions.
func bgErr(typ, detail string) *internal.ToolError {
	return &internal.ToolError{
		Tool: "bg_exec", Type: typ, Detail: detail,
		HintText: "Use action=list to see active processes.",
	}
}
