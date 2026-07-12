// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package background implements a durable background-process manager.
//
// It tracks task metadata (PID, status, exit code, output paths) in a JSON
// registry on disk so tasks survive agent restarts, and provides cross-platform
// process termination.
package background

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
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
	stdout *ringBuffer
	stderr *ringBuffer
	wg     sync.WaitGroup
}

// NewManager creates a manager that persists to path. If path is empty,
// the manager is in-memory only.
func NewManager(path string) (*Manager, error) {
	m := &Manager{
		path:  path,
		tasks: make(map[string]*Task),
		procs: make(map[string]*runningProc),
	}
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, fmt.Errorf("background manager: create dir: %w", err)
		}
		if err := m.load(); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("background manager: load: %w", err)
		}
	}
	return m, nil
}

// Start launches a background command and registers it in the task list.
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
		_ = os.MkdirAll(outputDir, 0755)
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
	m.procs[id] = proc
	m.mu.Unlock()

	proc.wg.Add(2)
	go m.collectOutput(proc, stdoutPipe, proc.stdout)
	go m.collectOutput(proc, stderrPipe, proc.stderr)
	go m.waitForExit(task, proc)
	if err := m.persist(); err != nil {
		return task, fmt.Errorf("background manager: persist: %w", err)
	}
	return task, nil
}

// Stop terminates a running task. It first sends SIGTERM, waits for a grace
// period, then escalates to SIGKILL.
func (m *Manager) Stop(id string, grace time.Duration) (*Task, error) {
	m.mu.Lock()
	proc, ok := m.procs[id]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("background manager: task %q not found", id)
	}
	m.terminate(proc, grace)
	m.mu.Lock()
	task := m.tasks[id]
	m.mu.Unlock()
	return task, nil
}

// List returns a snapshot of all registered tasks.
func (m *Manager) List() []*Task {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		cp := *t
		out = append(out, &cp)
	}
	return out
}

// Get returns a copy of a task by ID or nil if not found.
func (m *Manager) Get(id string) *Task {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if t := m.tasks[id]; t != nil {
		cp := *t
		return &cp
	}
	return nil
}

// ReadOutput returns the last n lines of stdout and stderr for a task.
func (m *Manager) ReadOutput(id string, n int) ([]string, []string) {
	m.mu.RLock()
	proc, ok := m.procs[id]
	m.mu.RUnlock()
	if !ok {
		return nil, nil
	}
	return proc.stdout.ReadLast(n), proc.stderr.ReadLast(n)
}

// WriteInput writes a line to the stdin of a running task.
func (m *Manager) WriteInput(id, text string) error {
	m.mu.RLock()
	proc, ok := m.procs[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("background manager: task %q not found", id)
	}
	_, err := io.WriteString(proc.stdin, text+"\n")
	return err
}

// StopAll terminates every running task.
func (m *Manager) StopAll(grace time.Duration) {
	m.mu.RLock()
	running := make([]*runningProc, 0, len(m.procs))
	for _, proc := range m.procs {
		running = append(running, proc)
	}
	m.mu.RUnlock()
	for _, proc := range running {
		m.terminate(proc, grace)
	}
}

func (m *Manager) waitForExit(task *Task, proc *runningProc) {
	exitErr := proc.cmd.Wait()
	proc.wg.Wait()
	endTime := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if exitErr != nil {
		task.Status = StatusError
	} else {
		task.Status = StatusCompleted
	}
	if proc.cmd.ProcessState != nil {
		task.ExitCode = proc.cmd.ProcessState.ExitCode()
	}
	task.EndTime = &endTime
	_ = m.persist()
	close(proc.done)
}

func (m *Manager) terminate(proc *runningProc, grace time.Duration) {
	select {
	case <-proc.done:
		return
	default:
	}
	_ = proc.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-proc.done:
	case <-time.After(grace):
		_ = proc.cmd.Process.Signal(syscall.SIGKILL)
		<-proc.done
	}
}

func (m *Manager) collectOutput(proc *runningProc, r io.ReadCloser, buf *ringBuffer) {
	defer proc.wg.Done()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		buf.Write(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		buf.Write(fmt.Sprintf("[background: read error: %v]", err))
	}
}

func (m *Manager) persist() error {
	if m.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(m.tasks, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.path, data, 0644)
}

func (m *Manager) load() error {
	data, err := os.ReadFile(m.path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &m.tasks)
}

func (m *Manager) outputDir(id string) string {
	if m.path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(m.path), "tasks", id)
}

func envPairs(env map[string]string) []string {
	pairs := make([]string, 0, len(env))
	for k, v := range env {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	return pairs
}

// ringBuffer is a circular buffer of text lines.
type ringBuffer struct {
	mu    sync.Mutex
	buf   []string
	size  int
	pos   int
	count int
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{buf: make([]string, size), size: size}
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
