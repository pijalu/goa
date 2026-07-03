// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Profile harness for goa.
//
// It can run the TUI engine in-process under a synthetic streaming load, or
// it can build the real goa binary and run it inside a PTY. In PTY mode you
// can either use the built-in performance load or send a real prompt and let
// goa execute normally while profiles are captured. Profiles are written after
// execution finishes so you can analyze them with go tool pprof.
//
// Usage:
//   go run ./cmd/profile                           # synthetic TUI load
//   go run ./cmd/profile -mode=pty                 # real goa binary in a PTY
//   go run ./cmd/profile -mode=pty -prompt="analyse this project"
//   go tool pprof -http=:8080 cpu.prof
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/pijalu/goa/tui"
)

func main() {
	cfg := parseFlags()

	var err error
	switch cfg.mode {
	case "synthetic":
		err = runSynthetic(cfg)
	case "pty":
		err = runPTY(cfg)
	default:
		err = fmt.Errorf("unknown mode %q (use synthetic or pty)", cfg.mode)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "profile: %v\n", err)
		os.Exit(1)
	}
}

type config struct {
	mode         string
	cpuFile      string
	memFile      string
	traceFile    string
	duration     time.Duration
	messageCount int
	termW        int
	termH        int
	updateRate   int
	prompt       string
	ptyLogFile   string
	keepBinary   bool
}

func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.mode, "mode", "synthetic", "harness mode: synthetic (in-process) or pty (real goa binary)")
	flag.StringVar(&cfg.cpuFile, "cpu", "cpu.prof", "write CPU profile to `file`")
	flag.StringVar(&cfg.memFile, "mem", "mem.prof", "write memory profile to `file`")
	flag.StringVar(&cfg.traceFile, "trace", "trace.out", "write execution trace to `file`")
	flag.DurationVar(&cfg.duration, "duration", 30*time.Second, "how long to run the synthetic load")
	flag.IntVar(&cfg.messageCount, "messages", 1000, "number of streaming updates to inject (synthetic mode)")
	flag.IntVar(&cfg.termW, "width", 120, "terminal width")
	flag.IntVar(&cfg.termH, "height", 40, "terminal height")
	flag.IntVar(&cfg.updateRate, "rate", 60, "target streaming updates per second (synthetic mode)")
	flag.StringVar(&cfg.prompt, "prompt", "", "prompt to send to the real goa binary in PTY mode (if empty, uses --perf-load)")
	flag.StringVar(&cfg.ptyLogFile, "pty-log", "pty.log", "where to save PTY output (pty mode)")
	flag.BoolVar(&cfg.keepBinary, "keep-binary", false, "keep the temporary goa binary after the run (pty mode)")
	flag.Parse()
	return cfg
}

// ── Synthetic mode ──────────────────────────────────────────────────────────

// recordingTerminal implements tui.Terminal with an in-memory buffer so the
// profile harness can run the TUI engine without a real PTY. It keeps the same
// I/O surface as ProcessTerminal so the render loop is exercised.
type recordingTerminal struct {
	w, h     int
	onInput  func(string)
	onResize func()
	mu       struct {
		sync.Mutex
		writes []string
		bytes  int64
	}
}

func (t *recordingTerminal) Start(onInput func(string), onResize func()) {
	t.onInput = onInput
	t.onResize = onResize
}
func (t *recordingTerminal) Stop()                                 {}
func (t *recordingTerminal) Write(p []byte) (int, error)             { t.record(string(p)); return len(p), nil }
func (t *recordingTerminal) WriteString(s string)                    { t.record(s) }
func (t *recordingTerminal) Size() (int, int)                        { return t.w, t.h }
func (t *recordingTerminal) SetRaw() (func(), error)                 { return func() {}, nil }
func (t *recordingTerminal) HideCursor()                             {}
func (t *recordingTerminal) ShowCursor()                             {}
func (t *recordingTerminal) ClearScreen()                            {}
func (t *recordingTerminal) SetTitle(string)                        {}
func (t *recordingTerminal) record(s string)                        {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.mu.writes = append(t.mu.writes, s)
	t.mu.bytes += int64(len(s))
}

func (t *recordingTerminal) stats() (writes int, bytes int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.mu.writes), t.mu.bytes
}

func runSynthetic(cfg config) error {
	stopCPU, err := startCPUProfile(cfg.cpuFile)
	if err != nil {
		return err
	}
	defer stopCPU()

	stopTrace, err := startTrace(cfg.traceFile)
	if err != nil {
		return err
	}
	defer stopTrace()

	term := &recordingTerminal{w: cfg.termW, h: cfg.termH}
	engine := tui.NewTUI(term)
	chat := tui.NewChatViewport()
	editor := tui.NewEditor()
	footer := tui.NewFooter()
	editor.SetFocused(true)

	engine.AddChild(chat)
	engine.AddChild(footer)
	engine.AddChild(editor)
	engine.SetFocus(editor)

	if err := engine.Start(); err != nil {
		return fmt.Errorf("start TUI: %w", err)
	}
	engine.RunLoops()

	engine.Apply(func() {
		chat.AddAssistantMessage("Analyzing project...")
	})

	startUsage := rusageNow()
	start := time.Now()

	var updates atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		interval := time.Second / time.Duration(max(cfg.updateRate, 1))
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for i := 0; i < cfg.messageCount; i++ {
			select {
			case <-ticker.C:
			case <-engine.Stopped():
				return
			}
			line := fmt.Sprintf("Streaming line %d with enough text to exercise wrapping and ansi handling across the viewport.", i)
			engine.Apply(func() {
				chat.UpdateLastMessage(line, tui.ConsoleAssistantMessage)
			})
			updates.Add(1)
		}
	}()

	select {
	case <-done:
	case <-time.After(cfg.duration):
	}

	engine.Stop()
	<-engine.Stopped()

	elapsed := time.Since(start)
	endUsage := rusageNow()
	writes, bytes := term.stats()

	if err := writeMemProfile(cfg.memFile); err != nil {
		return err
	}

	cpuSec := rusageSeconds(endUsage) - rusageSeconds(startUsage)
	cpuPct := 100 * cpuSec / elapsed.Seconds()

	fmt.Println("Synthetic TUI profile run complete.")
	fmt.Printf("  Duration:        %v\n", elapsed.Round(time.Millisecond))
	fmt.Printf("  Updates:         %d\n", updates.Load())
	fmt.Printf("  Terminal writes: %d (%d bytes)\n", writes, bytes)
	fmt.Printf("  CPU time:        %.3fs (%.1f%% of wall time)\n", cpuSec, cpuPct)
	fmt.Printf("  CPU profile:     %s\n", cfg.cpuFile)
	fmt.Printf("  MEM profile:     %s\n", cfg.memFile)
	fmt.Printf("  Trace:           %s\n", cfg.traceFile)
	if cpuPct > 15 {
		fmt.Printf("\nWARNING: CPU usage %.1f%% exceeds the 15%% target.\n", cpuPct)
		fmt.Println("Open the CPU profile in pprof to identify the bottleneck:")
		fmt.Printf("  go tool pprof -http=:8080 %s\n", cfg.cpuFile)
	}
	return nil
}

// ── PTY mode ────────────────────────────────────────────────────────────────

func runPTY(cfg config) error {
	start := time.Now()

	binary, cleanup, err := buildTempGoaBinary(cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	args := profileArgs(cfg)
	if cfg.prompt == "" {
		args = append(args, "--perf-load", "--perf-load-duration", cfg.duration.String())
	}

	cmd := exec.Command(binary, args...)
	cmd.Dir = projectRoot()
	cmd.Env = os.Environ()

	ptym, copyDone, err := startPTY(cmd, cfg)
	if err != nil {
		return err
	}
	defer ptym.Close()

	if cfg.prompt != "" {
		if err := interactWithPTY(ptym, cfg); err != nil {
			return err
		}
	}

	if err := waitForCommand(cmd, 30*time.Second); err != nil {
		return fmt.Errorf("goa exited: %w", err)
	}
	<-copyDone

	elapsed := time.Since(start)
	childUsage, _ := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	reportPTYResults(elapsed, cfg, childUsage)
	return nil
}

// buildTempGoaBinary builds the goa binary in a temp directory and returns the
// path plus a cleanup function. With --keep-binary the binary is kept and the
// cleanup is a no-op.
func buildTempGoaBinary(cfg config) (binary string, cleanup func(), err error) {
	tmpDir, err := os.MkdirTemp("", "goa-profile-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}
	cleanup = func() { os.RemoveAll(tmpDir) }
	binary = filepath.Join(tmpDir, "goa")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}

	buildCmd := exec.Command("go", "build", "-o", binary, "./cmd/goa")
	buildCmd.Dir = projectRoot()
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	fmt.Println("Building goa binary for PTY profile...")
	if err := buildCmd.Run(); err != nil {
		return "", cleanup, fmt.Errorf("build goa binary: %w", err)
	}
	if cfg.keepBinary {
		fmt.Printf("Keeping built binary at %s\n", binary)
		cleanup = func() {}
	}
	return binary, cleanup, nil
}

// profileArgs builds the profiling flag slice passed to the goa binary under test.
func profileArgs(cfg config) []string {
	var args []string
	if cfg.cpuFile != "" {
		args = append(args, "--cpuprofile", cfg.cpuFile)
	}
	if cfg.memFile != "" {
		args = append(args, "--memprofile", cfg.memFile)
	}
	if cfg.traceFile != "" {
		args = append(args, "--trace", cfg.traceFile)
	}
	return args
}

// startPTY starts cmd inside a PTY, captures the PTY output to pty.log, and
// returns the PTY master file together with a channel that is closed once the
// output capture goroutine has finished.
func startPTY(cmd *exec.Cmd, cfg config) (*os.File, <-chan struct{}, error) {
	ptym, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: uint16(cfg.termW),
		Rows: uint16(cfg.termH),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("start pty: %w", err)
	}

	logFile, err := os.Create(cfg.ptyLogFile)
	if err != nil {
		ptym.Close()
		return nil, nil, fmt.Errorf("create pty log: %w", err)
	}

	copyDone := make(chan struct{})
	go func() {
		defer close(copyDone)
		defer logFile.Close()
		io.Copy(logFile, ptym)
	}()

	return ptym, copyDone, nil
}

// interactWithPTY sends the user prompt to the PTY after a short startup delay,
// then sends Ctrl+C after the configured duration to end the session.
func interactWithPTY(ptym *os.File, cfg config) error {
	time.Sleep(500 * time.Millisecond)
	if _, err := ptym.WriteString(cfg.prompt + "\n"); err != nil {
		return fmt.Errorf("write prompt: %w", err)
	}
	go func() {
		time.Sleep(cfg.duration)
		ptym.WriteString("\x03")
	}()
	return nil
}

// waitForCommand waits for cmd to exit. If it does not exit within timeout it
// is killed so the harness can still collect the profiles that were written up
// to that point.
func waitForCommand(cmd *exec.Cmd, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		return <-done
	}
}

// reportPTYResults prints the run summary and warns if CPU usage exceeds the
// 15% target.
func reportPTYResults(elapsed time.Duration, cfg config, childUsage *syscall.Rusage) {
	cpuSec := rusageSeconds(childUsage)
	cpuPct := 100 * cpuSec / elapsed.Seconds()

	fmt.Println("PTY profile run complete.")
	fmt.Printf("  Duration:     %v\n", elapsed.Round(time.Millisecond))
	fmt.Printf("  CPU time:     %.3fs (%.1f%% of wall time)\n", cpuSec, cpuPct)
	fmt.Printf("  PTY output:   %s\n", cfg.ptyLogFile)
	fmt.Printf("  CPU profile:  %s\n", cfg.cpuFile)
	fmt.Printf("  MEM profile:  %s\n", cfg.memFile)
	fmt.Printf("  Trace:        %s\n", cfg.traceFile)
	if cpuPct > 15 {
		fmt.Printf("\nWARNING: CPU usage %.1f%% exceeds the 15%% target.\n", cpuPct)
		fmt.Println("Open the CPU profile in pprof to identify the bottleneck:")
		fmt.Printf("  go tool pprof -http=:8080 %s\n", cfg.cpuFile)
	}
}

// projectRoot returns the repository root by assuming cmd/profile is run from
// the project root. If it is run from another directory, this can be replaced
// by go env resolution.
func projectRoot() string {
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

// ── Profiling helpers ───────────────────────────────────────────────────────

func startCPUProfile(path string) (func(), error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create cpu profile: %w", err)
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("start cpu profile: %w", err)
	}
	return func() {
		pprof.StopCPUProfile()
		f.Close()
	}, nil
}

func startTrace(path string) (func(), error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create trace: %w", err)
	}
	if err := trace.Start(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("start trace: %w", err)
	}
	return func() {
		trace.Stop()
		f.Close()
	}, nil
}

func writeMemProfile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create mem profile: %w", err)
	}
	defer f.Close()
	runtime.GC()
	if err := pprof.WriteHeapProfile(f); err != nil {
		return fmt.Errorf("write heap profile: %w", err)
	}
	return nil
}

func rusageNow() *syscall.Rusage {
	var r syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &r); err != nil {
		return &syscall.Rusage{}
	}
	return &r
}

func rusageSeconds(r *syscall.Rusage) float64 {
	if r == nil {
		return 0
	}
	return float64(r.Utime.Sec) + float64(r.Utime.Usec)/1e6 +
		float64(r.Stime.Sec) + float64(r.Stime.Usec)/1e6
}
