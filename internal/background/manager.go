// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package background implements a durable background-process manager.
//
// It tracks task metadata (PID, status, exit code, output paths) in a JSON
// registry on disk so tasks survive agent restarts, and provides cross-platform
// process termination. Output is teed to per-task log files so it can be read
// back after a restart even when the original pipes are gone.
package background

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Status is the lifecycle state of a background task.
type Status string

const (
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusError     Status = "error"
	StatusKilled    Status = "killed"
)

// Task records the metadata for a background process.
type Task struct {
	ID        string            `json:"id"`
	Command   string            `json:"command"`
	PID       int               `json:"pid"`
	Status    Status            `json:"status"`
	ExitCode  int               `json:"exit_code,omitempty"`
	StartTime time.Time         `json:"start_time"`
	EndTime   *time.Time        `json:"end_time,omitempty"`
	Workdir   string            `json:"workdir,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	OutputDir string            `json:"output_dir"`
}

// Manager is a durable registry of background tasks.
type Manager struct {
	mu      sync.RWMutex
	path    string
	tasks   map[string]*Task
	procs   map[string]*runningProc
	counter int
}

// runningProc holds the runtime handles for an active task.
type runningProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	done   chan struct{}
	killed atomic.Bool
	wg     sync.WaitGroup
	stdout *ringBuffer
	stderr *ringBuffer
	outW   *teeWriter
	errW   *teeWriter
}

// stdoutLog / stderrLog name the teed output files inside a task's OutputDir.
const (
	stdoutLog = "stdout.log"
	stderrLog = "stderr.log"
)

// NewManager creates a manager that persists to path. If path is empty,
// the manager is in-memory only.
func NewManager(path string) (*Manager, error) {
	m := &Manager{
		path:  path,
		tasks: make(map[string]*Task),
		procs: make(map[string]*runningProc),
	}
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("background manager: create dir: %w", err)
		}
		if err := m.load(); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("background manager: load: %w", err)
		}
	}
	return m, nil
}

// Start launches a background command and registers it in the task list.
// The returned Task is a snapshot; callers may inspect it without holding a
// lock. Mutating it does not affect the manager's internal state.
func (m *Manager) Start(command, workdir string, env map[string]string) (*Task, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	cmd := exec.Command(shell, "-c", command)
	if workdir != "" {
		cmd.Dir = workdir
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), envPairs(env)...)
	}
	setSysProcAttr(cmd)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("background manager: stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("background manager: stderr pipe: %w", err)
	}
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("background manager: stdin pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("background manager: start: %w", err)
	}

	m.mu.Lock()
	m.counter++
	id := fmt.Sprintf("proc-%d", m.counter)
	outputDir := m.outputDir(id)
	if outputDir != "" {
		_ = os.MkdirAll(outputDir, 0o755)
	}
	task := &Task{
		ID:        id,
		Command:   command,
		PID:       cmd.Process.Pid,
		Status:    StatusRunning,
		StartTime: time.Now(),
		Workdir:   workdir,
		Env:       env,
		OutputDir: outputDir,
	}
	m.tasks[id] = task
	proc := &runningProc{
		cmd:    cmd,
		stdin:  stdinPipe,
		done:   make(chan struct{}),
		stdout: newRingBuffer(10000),
		stderr: newRingBuffer(10000),
	}
	proc.outW = newTeeWriter(filepath.Join(outputDir, stdoutLog), proc.stdout)
	proc.errW = newTeeWriter(filepath.Join(outputDir, stderrLog), proc.stderr)
	m.procs[id] = proc
	persistErr := m.persistLocked()
	returned := cloneTask(task)
	m.mu.Unlock()

	proc.wg.Add(2)
	go m.collectOutput(proc, stdoutPipe, proc.outW)
	go m.collectOutput(proc, stderrPipe, proc.errW)
	go m.waitForExit(task, proc)

	if persistErr != nil {
		return returned, fmt.Errorf("background manager: persist: %w", persistErr)
	}
	return returned, nil
}

// Stop terminates a running task. It first sends a graceful signal, waits for
// a grace period, then escalates to a forceful kill. Tasks reattached after a
// restart (live PID, no captured pipes) are killed directly by PID.
func (m *Manager) Stop(id string, grace time.Duration) (*Task, error) {
	m.mu.Lock()
	proc, hasProc := m.procs[id]
	task := m.tasks[id]
	m.mu.Unlock()

	if task == nil && !hasProc {
		return nil, fmt.Errorf("background manager: task %q not found", id)
	}
	if !hasProc {
		m.stopReattached(task, grace)
		m.mu.Lock()
		t := cloneTask(m.tasks[id])
		m.mu.Unlock()
		return t, nil
	}
	m.terminate(proc, grace)
	m.mu.Lock()
	t := cloneTask(m.tasks[id])
	m.mu.Unlock()
	return t, nil
}

// List returns a snapshot of all registered tasks.
func (m *Manager) List() []*Task {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, cloneTask(t))
	}
	return out
}

// Get returns a copy of a task by ID or nil if not found.
func (m *Manager) Get(id string) *Task {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if t := m.tasks[id]; t != nil {
		return cloneTask(t)
	}
	return nil
}

// ReadOutput returns the last n lines of stdout and stderr for a task. For a
// live task it reads the in-memory ring buffer; after a restart it tails the
// persisted log files written under OutputDir.
func (m *Manager) ReadOutput(id string, n int) ([]string, []string) {
	m.mu.RLock()
	proc, ok := m.procs[id]
	m.mu.RUnlock()
	if ok {
		return proc.stdout.ReadLast(n), proc.stderr.ReadLast(n)
	}
	task := m.Get(id)
	if task == nil || task.OutputDir == "" {
		return nil, nil
	}
	return tailLines(filepath.Join(task.OutputDir, stdoutLog), n),
		tailLines(filepath.Join(task.OutputDir, stderrLog), n)
}

// WriteInput writes a line to the stdin of a running task. Reattached tasks
// have no stdin pipe and return an error.
func (m *Manager) WriteInput(id, text string) error {
	m.mu.RLock()
	proc, ok := m.procs[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("background manager: task %q has no stdin (not running)", id)
	}
	_, err := io.WriteString(proc.stdin, text+"\n")
	return err
}

// StopAll terminates every running task, including tasks reattached after a
// restart whose pipes are no longer available.
func (m *Manager) StopAll(grace time.Duration) {
	m.mu.Lock()
	type liveProc struct {
		proc *runningProc
		task *Task
	}
	var live []liveProc
	for id, proc := range m.procs {
		live = append(live, liveProc{proc: proc, task: m.tasks[id]})
	}
	// Reattached tasks: running but no captured proc handle.
	for id, t := range m.tasks {
		if _, ok := m.procs[id]; ok {
			continue
		}
		if t.Status == StatusRunning {
			live = append(live, liveProc{task: t})
		}
	}
	m.mu.Unlock()

	for _, lp := range live {
		if lp.proc != nil {
			m.terminate(lp.proc, grace)
		} else if lp.task != nil {
			m.stopReattached(lp.task, grace)
		}
	}
}

// waitForExit waits for the process to exit, then records the final status.
// A terminated process is marked StatusKilled; other non-zero exits are
// StatusError; clean exits are StatusCompleted. The killed flag is set by
// terminate() before signalling, so it is reliably visible here.
func (m *Manager) waitForExit(task *Task, proc *runningProc) {
	exitErr := proc.cmd.Wait()
	proc.wg.Wait()
	close(proc.outW.done)
	close(proc.errW.done)
	_ = proc.stdin.Close()

	endTime := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case proc.killed.Load():
		task.Status = StatusKilled
	case exitErr != nil:
		task.Status = StatusError
	default:
		task.Status = StatusCompleted
	}
	if proc.cmd.ProcessState != nil {
		task.ExitCode = proc.cmd.ProcessState.ExitCode()
	}
	task.EndTime = &endTime
	_ = m.persistLocked()
	close(proc.done)
}

func (m *Manager) terminate(proc *runningProc, grace time.Duration) {
	select {
	case <-proc.done:
		return
	default:
	}
	proc.killed.Store(true)
	pid := proc.cmd.Process.Pid
	_ = signalProcess(pid, false) // SIGTERM / Ctrl-Break
	select {
	case <-proc.done:
	case <-time.After(grace):
		_ = signalProcess(pid, true) // SIGKILL / taskkill /F
		<-proc.done
	}
}

// stopReattached kills a task whose pipes are gone (loaded after a restart)
// by PID, escalating from graceful to forceful after grace.
func (m *Manager) stopReattached(task *Task, grace time.Duration) {
	if task == nil || task.PID <= 0 {
		return
	}
	if !pidAlive(task.PID) {
		m.markReattached(task.ID, StatusCompleted)
		return
	}
	_ = signalProcess(task.PID, false)
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) && pidAlive(task.PID) {
		time.Sleep(50 * time.Millisecond)
	}
	if pidAlive(task.PID) {
		_ = signalProcess(task.PID, true)
	}
	m.markReattached(task.ID, StatusKilled)
}

func (m *Manager) markReattached(id string, status Status) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return
	}
	end := time.Now()
	t.Status = status
	t.ExitCode = -1
	t.EndTime = &end
	_ = m.persistLocked()
}

func (m *Manager) collectOutput(proc *runningProc, r io.ReadCloser, w *teeWriter) {
	defer proc.wg.Done()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		w.line(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		w.line(fmt.Sprintf("[background: read error: %v]", err))
	}
}

// persistLocked writes the registry atomically. Caller must hold m.mu.
func (m *Manager) persistLocked() error {
	if m.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(m.tasks, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(m.path, data, 0o644)
}

func (m *Manager) load() error {
	data, err := os.ReadFile(m.path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &m.tasks); err != nil {
		return err
	}
	m.reconcileLoaded()
	return nil
}

// reconcileLoaded reconstructs the ID counter from persisted task IDs and
// reconciles the status of tasks that were still running when the previous
// process exited.
func (m *Manager) reconcileLoaded() {
	for id, t := range m.tasks {
		if n := procNumber(id); n > m.counter {
			m.counter = n
		}
		if t.Status != StatusRunning {
			continue
		}
		// A task left "running" by a prior process: either it died with the
		// manager, or it is still alive (orphaned). Probe and settle.
		if t.PID <= 0 || !pidAlive(t.PID) {
			t.Status = StatusError
			t.ExitCode = -1
			end := time.Now()
			t.EndTime = &end
		}
		// If still alive, leave it Running so Stop/StopAll can kill it by PID.
	}
}

var procIDRe = regexp.MustCompile(`^proc-(\d+)$`)

func procNumber(id string) int {
	m := procIDRe.FindStringSubmatch(id)
	if len(m) != 2 {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// cloneTask returns a defensive copy of a task. The caller must hold the
// manager lock (or otherwise guarantee no concurrent mutation) when reading t.
func cloneTask(t *Task) *Task {
	if t == nil {
		return nil
	}
	cp := *t
	return &cp
}

func (m *Manager) outputDir(id string) string {
	if m.path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(m.path), "tasks", id)
}

