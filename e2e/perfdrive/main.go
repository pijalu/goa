// Command perfdrive runs the goa TUI inside a PTY, submits a prompt, and
// samples the process CPU%/RSS at a fixed cadence until a completion marker
// appears — a purpose-built harness for the "CPU must not spike over 10-20%
// during streaming" performance check.
//
// It differs from ptydrive (same PTY mechanics) in what it observes: instead
// of waiting for a file condition, it records a time series of
//
//	ps -o %cpu=,rss= -p PID
//
// samples and prints max / avg / p95 CPU plus peak RSS. %cpu from ps is a
// decaying average — the same figure Activity Monitor shows — so it matches
// what a user perceives as a "spike" while smoothing single-tick noise.
//
// Usage:
//
//	perfdrive --bin <goa> --dir <workdir> --log <rawlog> --csv <samples.csv> \
//	  [--prompt "text"] [--settle 12s] [--wait-file <marker>] [--grace 5s] \
//	  [--duration 30s] [--sample 1s] [--rows 48] [--cols 160]
//
// With --prompt empty nothing is sent (idle baseline): sampling then runs for
// --duration. With --wait-file set, sampling ends when the marker exists plus
// --grace (stream done, UI settled). The TUI is closed with /quit + Enter and
// force-killed after a short grace if it does not exit.
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

// sample is one CPU/RSS observation of the driven process.
type sample struct {
	t   time.Duration // elapsed since sampling start
	cpu float64       // ps decaying-average %cpu
	rss int64         // resident size, KB
}

func main() {
	bin := flag.String("bin", "", "binary to run (required)")
	dir := flag.String("dir", ".", "working directory")
	logPath := flag.String("log", "", "raw PTY output log (required)")
	csvPath := flag.String("csv", "", "sample CSV output (required)")
	prompt := flag.String("prompt", "", "prompt to submit (empty = idle baseline)")
	settle := flag.Duration("settle", 12*time.Second, "wait after start before sending the prompt")
	waitFile := flag.String("wait-file", "", "marker file that ends sampling (with --grace)")
	grace := flag.Duration("grace", 5*time.Second, "extra sampling after the marker appears")
	duration := flag.Duration("duration", 30*time.Second, "sampling length when no --wait-file (idle baseline)")
	sampleEvery := flag.Duration("sample", 1*time.Second, "sampling cadence")
	rows := flag.Uint("rows", 48, "PTY rows")
	cols := flag.Uint("cols", 160, "PTY columns")
	flag.Parse()

	if *bin == "" || *logPath == "" || *csvPath == "" {
		fmt.Fprintln(os.Stderr, "required: --bin --log --csv")
		os.Exit(2)
	}
	absBin, err := filepath.Abs(*bin)
	if err != nil {
		fatal(err)
	}
	logf, err := os.Create(*logPath)
	if err != nil {
		fatal(err)
	}
	defer logf.Close()

	cmd := exec.Command(absBin)
	cmd.Dir = *dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(*rows), Cols: uint16(*cols)})
	if err != nil {
		fatal(fmt.Errorf("pty start: %w", err))
	}
	defer ptmx.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // drain PTY output to the raw log; exits when the child dies
		defer wg.Done()
		buf := make([]byte, 65536)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				_, _ = logf.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	pid := cmd.Process.Pid
	fmt.Printf("started pid=%d settle=%v\n", pid, *settle)
	time.Sleep(*settle)

	if *prompt != "" {
		if _, err := ptmx.WriteString(*prompt + "\r"); err != nil {
			fatal(fmt.Errorf("send prompt: %w", err))
		}
		fmt.Printf("prompt sent (%d chars)\n", len(*prompt))
	}

	start := time.Now()
	var samples []sample
	deadline := start.Add(*duration)
	if *waitFile != "" {
		deadline = start.Add(2 * time.Hour) // bounded by marker + grace
	}
	var markerSeenAt time.Time
	for time.Now().Before(deadline) {
		time.Sleep(*sampleEvery)
		if s, ok := sampleProc(pid); ok {
			s.t = time.Since(start)
			samples = append(samples, s)
			fmt.Printf("\r  t=%4.0fs cpu=%5.1f%% rss=%4.0fMB", s.t.Seconds(), s.cpu, float64(s.rss)/1024)
		}
		if cmd.ProcessState != nil {
			break
		}
		if *waitFile == "" {
			continue
		}
		if _, err := os.Stat(*waitFile); err == nil {
			if markerSeenAt.IsZero() {
				markerSeenAt = time.Now()
				fmt.Printf("\n  marker seen; sampling %v more\n", *grace)
			}
			if time.Since(markerSeenAt) >= *grace {
				break
			}
		}
	}
	fmt.Println()

	writeCSV(*csvPath, samples)
	report(samples)

	// Close the TUI politely, then force-kill.
	_, _ = ptmx.WriteString("/quit\r")
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
		fmt.Println("clean exit")
	case <-time.After(8 * time.Second):
		_ = cmd.Process.Kill()
		fmt.Println("killed after /quit grace")
	}
	wg.Wait()
}

// sampleProc reads ps's decaying %cpu average and resident size for pid.
func sampleProc(pid int) (sample, bool) {
	out, err := exec.Command("ps", "-o", "cpu=,rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return sample{}, false
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return sample{}, false
	}
	cpu, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return sample{}, false
	}
	rss, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return sample{}, false
	}
	return sample{cpu: cpu, rss: rss}, true
}

func writeCSV(path string, samples []sample) {
	f, err := os.Create(path)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"t_seconds", "cpu_percent", "rss_kb"})
	for _, s := range samples {
		_ = w.Write([]string{
			strconv.FormatFloat(s.t.Seconds(), 'f', 1, 64),
			strconv.FormatFloat(s.cpu, 'f', 2, 64),
			strconv.FormatInt(s.rss, 10),
		})
	}
	w.Flush()
}

func report(samples []sample) {
	if len(samples) == 0 {
		fmt.Println("no samples")
		return
	}
	cpus := make([]float64, len(samples))
	var sum float64
	var maxRSS int64
	for i, s := range samples {
		cpus[i] = s.cpu
		sum += s.cpu
		if s.rss > maxRSS {
			maxRSS = s.rss
		}
	}
	sort.Float64s(cpus)
	pct := func(q float64) float64 {
		i := int(float64(len(cpus)-1) * q)
		return cpus[i]
	}
	fmt.Printf("samples=%d  avg=%.1f%%  p50=%.1f%%  p95=%.1f%%  max=%.1f%%  peakRSS=%.0fMB\n",
		len(samples), sum/float64(len(samples)), pct(0.50), pct(0.95), cpus[len(cpus)-1], float64(maxRSS)/1024)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "perfdrive:", err)
	os.Exit(1)
}
