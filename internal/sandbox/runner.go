// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// RunOpts configures a sandboxed subprocess run.
type RunOpts struct {
	Cmd       []string
	Workdir   string
	Env       map[string]string
	Timeout   time.Duration
	MaxOutput int
	Cancel    context.Context
	Preexec   func() error
}

// RunResult holds the outcome of a sandboxed subprocess.
type RunResult struct {
	Output   string
	ExitCode int
	TimedOut bool
}

// Run executes a command in a sandboxed subprocess.
func Run(opts RunOpts) (RunResult, error) {
	if len(opts.Cmd) == 0 {
		return RunResult{}, fmt.Errorf("sandbox: no command")
	}

	ctx := opts.Cancel
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, opts.Cmd[0], opts.Cmd[1:]...)
	cmd.Dir = opts.Workdir
	cmd.Env = envSlice(opts.Env)
	applyPreexec(cmd, opts.Preexec)

	stdout, stderr, err := startCommand(cmd)
	if err != nil {
		return RunResult{}, err
	}

	watchDone := startProcessWatcher(ctx, cmd)
	defer close(watchDone)

	var out bytes.Buffer
	_, _ = copyOutput(&out, stdout, stderr, opts.MaxOutput)
	return buildResult(cmd, out.String(), ctx.Err())
}

func startCommand(cmd *exec.Cmd) (io.ReadCloser, io.ReadCloser, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("sandbox: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("sandbox: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("sandbox: exec: %w", err)
	}
	return stdout, stderr, nil
}

func startProcessWatcher(ctx context.Context, cmd *exec.Cmd) chan struct{} {
	watchDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			killProcessGroup(cmd)
		case <-watchDone:
		}
	}()
	return watchDone
}

func buildResult(cmd *exec.Cmd, output string, ctxErr error) (RunResult, error) {
	err := cmd.Wait()
	timedOut := ctxErr == context.DeadlineExceeded
	result := RunResult{Output: output, TimedOut: timedOut}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	} else if err != nil {
		result.ExitCode = -1
	}
	if err != nil && !timedOut && ctxErr == nil {
		return result, fmt.Errorf("sandbox: exec: %w", err)
	}
	return result, nil
}

// copyOutput drains stdout and stderr into dst, truncating if max > 0.
func copyOutput(dst *bytes.Buffer, stdout, stderr io.ReadCloser, max int) (int64, error) {
	var mu sync.Mutex
	var total int
	write := func(b []byte) {
		mu.Lock()
		defer mu.Unlock()
		if max > 0 && total+len(b) > max {
			keep := max - total
			if keep > 0 {
				dst.Write(b[:keep])
				total += keep
			}
			return
		}
		dst.Write(b)
		total += len(b)
	}

	var wg sync.WaitGroup
	copyOne := func(r io.ReadCloser) {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}
	wg.Add(2)
	go copyOne(stdout)
	go copyOne(stderr)
	wg.Wait()
	return int64(dst.Len()), nil
}

func truncateOutput(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	var b bytes.Buffer
	b.WriteString(s[:max])
	fmt.Fprintf(&b, "\n\n... (truncated, %d chars total)", len(s))
	return b.String()
}

func envSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
