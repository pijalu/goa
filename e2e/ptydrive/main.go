// ptydrive runs a binary (goa TUI) inside a PTY, sends keystrokes, and waits
// for a FILE condition (glob + regex over file content) instead of parsing
// TUI redraws. Built for e2e validation of TUI-only flows (e.g.
// /orchestrate:new, /companion:framework).
//
// Usage:
//
//	ptydrive --bin <path> --dir <workdir> --log <rawlog> \
//	  [--send "text"]... [--send-raw "bytes"]... [--send-delay 8s] \
//	  --wait-file <glob> --wait-pattern <regex> [--timeout 15m]
//
// Exit code 0 when the pattern matched, 1 on timeout/error. Raw PTY output is
// always written to --log for post-hoc inspection.
//
// --send types text followed by Enter. --send-raw writes the bytes as-is
// (no trailing Enter) so hotkeys can be driven without an immediate submit
// (e.g. 'e' edit in /goal:manage, which must not be followed by '\r' before
// the replacement text arrives). Both flags append to ONE ordered step list:
// interleaved --send/--send-raw flags execute in command-line order.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/creack/pty"
)

// sendStep is one scripted input write; enter appends a carriage return.
type sendStep struct {
	text  string
	enter bool
}

// sendList collects --send / --send-raw flags into a single ordered step
// list. The flag package calls Set in command-line order, so interleaved
// --send and --send-raw flags keep their relative order.
type sendList struct {
	steps *[]sendStep
	enter bool
}

func (s sendList) String() string { return fmt.Sprintf("%d steps", len(*s.steps)) }

func (s sendList) Set(v string) error {
	*s.steps = append(*s.steps, sendStep{text: v, enter: s.enter})
	return nil
}

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;?]*[a-zA-Z]|\x1b\\][^\x07]*\x07|\x1b[()][0-9A-Z]|\r")

func stripANSI(b []byte) string { return ansiRe.ReplaceAllString(string(b), "") }

func main() {
	var steps []sendStep
	bin := flag.String("bin", "", "binary to run (required)")
	dir := flag.String("dir", ".", "working directory")
	logPath := flag.String("log", "", "raw output log path (required)")
	flag.Var(sendList{&steps, true}, "send", "text to type followed by Enter (repeatable, sent in order)")
	flag.Var(sendList{&steps, false}, "send-raw", "bytes to type as-is, no trailing Enter (repeatable, ordered with --send)")
	sendDelay := flag.Duration("send-delay", 8*time.Second, "delay before each send")
	waitFile := flag.String("wait-file", "", "glob of file(s) to poll")
	waitPattern := flag.String("wait-pattern", "", "regex to match in the polled file content")
	timeout := flag.Duration("timeout", 15*time.Minute, "overall timeout")
	flag.Parse()

	if *bin == "" || *logPath == "" || *waitFile == "" || *waitPattern == "" {
		fmt.Fprintln(os.Stderr, "required: --bin --log --wait-file --wait-pattern")
		os.Exit(2)
	}
	if err := run(*bin, *dir, *logPath, steps, *sendDelay, *waitFile, *waitPattern, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, "ptydrive:", err)
		os.Exit(1)
	}
}

func run(bin, dir, logPath string, steps []sendStep, sendDelay time.Duration, waitFile, waitPattern string, timeout time.Duration) error {
	absBin, err := filepath.Abs(bin)
	if err != nil {
		return err
	}
	logf, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer logf.Close()

	cmd := exec.Command(absBin)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	ptty, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 160, Rows: 48})
	if err != nil {
		return fmt.Errorf("pty start: %w", err)
	}
	defer ptty.Close()

	var mu sync.Mutex
	var buf []byte
	go captureOutput(ptty, logf, &mu, &buf)

	re, err := regexp.Compile(waitPattern)
	if err != nil {
		return fmt.Errorf("bad --wait-pattern: %w", err)
	}

	deadline := time.Now().Add(timeout)
	switch err := sendSteps(ptty, steps, sendDelay, deadline); {
	case err == errDeadline:
		return killAndReport(cmd, ptty, &mu, &buf, "timeout before send complete")
	case err != nil:
		return err
	}
	return waitForCondition(cmd, ptty, &mu, &buf, waitFile, re, deadline)
}

// captureOutput streams PTY output into the raw log and the shared buffer
// (used for the failure tail). Returns when the PTY read fails (process exit).
func captureOutput(ptty *os.File, logf *os.File, mu *sync.Mutex, buf *[]byte) {
	chunk := make([]byte, 65536)
	for {
		n, err := ptty.Read(chunk)
		if n > 0 {
			mu.Lock()
			*buf = append(*buf, chunk[:n]...)
			mu.Unlock()
			_, _ = logf.Write(chunk[:n])
		}
		if err != nil {
			return
		}
	}
}

// errDeadline marks the overall deadline expiring mid-script — distinct from
// a PTY write failure: the former kills and reports, the latter just fails.
var errDeadline = fmt.Errorf("deadline exceeded")

// sendSteps writes the scripted steps to the PTY, sleeping sendDelay before
// each one. Steps with enter set get a trailing carriage return.
func sendSteps(ptty *os.File, steps []sendStep, sendDelay time.Duration, deadline time.Time) error {
	for _, s := range steps {
		if !sleepOrDeadline(sendDelay, deadline) {
			return errDeadline
		}
		text := s.text
		if s.enter {
			text += "\r"
		}
		if _, err := ptty.Write([]byte(text)); err != nil {
			return fmt.Errorf("send: %w", err)
		}
	}
	return nil
}

// waitForCondition polls the wait-file glob until the pattern matches, the
// process exits, or the deadline passes.
func waitForCondition(cmd *exec.Cmd, ptty *os.File, mu *sync.Mutex, buf *[]byte, waitFile string, re *regexp.Regexp, deadline time.Time) error {
	for time.Now().Before(deadline) {
		if matches, err := globAnyContains(waitFile, re); err == nil && matches {
			fmt.Println("ptydrive: condition met")
			gracefulStop(cmd, ptty)
			return nil
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return killAndReport(cmd, ptty, mu, buf, "process exited before condition met")
		}
		time.Sleep(2 * time.Second)
	}
	return killAndReport(cmd, ptty, mu, buf, "timeout waiting for condition")
}

func globAnyContains(glob string, re *regexp.Regexp) (bool, error) {
	files, err := filepath.Glob(glob)
	if err != nil || len(files) == 0 {
		return false, err
	}
	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if re.Match(content) {
			return true, nil
		}
	}
	return false, nil
}

func sleepOrDeadline(d time.Duration, deadline time.Time) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
	return true
}

func gracefulStop(cmd *exec.Cmd, ptty *os.File) {
	_, _ = ptty.Write([]byte{3}) // ctrl+c
	time.Sleep(2 * time.Second)
	_, _ = ptty.Write([]byte{3})
	time.Sleep(2 * time.Second)
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
}

func killAndReport(cmd *exec.Cmd, ptty *os.File, mu *sync.Mutex, buf *[]byte, why string) error {
	gracefulStop(cmd, ptty)
	mu.Lock()
	tail := stripANSI(*buf)
	mu.Unlock()
	if len(tail) > 3000 {
		tail = tail[len(tail)-3000:]
	}
	return fmt.Errorf("%s\n--- output tail ---\n%s", why, tail)
}
