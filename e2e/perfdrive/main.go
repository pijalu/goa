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

// driveConfig bundles every perfdrive invocation setting parsed from flags.
type driveConfig struct {
	bin         string        // --bin, required
	dir         string        // --dir working directory
	logPath     string        // --log raw PTY output log, required
	csvPath     string        // --csv sample CSV output, required
	prompt      string        // --prompt (empty = idle baseline)
	settle      time.Duration // --settle wait before sending the prompt
	waitFile    string        // --wait-file marker ending sampling (empty = timed)
	grace       time.Duration // --grace extra sampling after the marker
	duration    time.Duration // --duration sampling length without --wait-file
	sampleEvery time.Duration // --sample sampling cadence
	rows        uint          // --rows PTY rows
	cols        uint          // --cols PTY columns
}

// parseFlags builds the configuration from command-line flags, exiting with
// usage status 2 when required flags are missing.
func parseFlags() driveConfig {
	var cfg driveConfig
	flag.StringVar(&cfg.bin, "bin", "", "binary to run (required)")
	flag.StringVar(&cfg.dir, "dir", ".", "working directory")
	flag.StringVar(&cfg.logPath, "log", "", "raw PTY output log (required)")
	flag.StringVar(&cfg.csvPath, "csv", "", "sample CSV output (required)")
	flag.StringVar(&cfg.prompt, "prompt", "", "prompt to submit (empty = idle baseline)")
	flag.DurationVar(&cfg.settle, "settle", 12*time.Second, "wait after start before sending the prompt")
	flag.StringVar(&cfg.waitFile, "wait-file", "", "marker file that ends sampling (with --grace)")
	flag.DurationVar(&cfg.grace, "grace", 5*time.Second, "extra sampling after the marker appears")
	flag.DurationVar(&cfg.duration, "duration", 30*time.Second, "sampling length when no --wait-file (idle baseline)")
	flag.DurationVar(&cfg.sampleEvery, "sample", 1*time.Second, "sampling cadence")
	flag.UintVar(&cfg.rows, "rows", 48, "PTY rows")
	flag.UintVar(&cfg.cols, "cols", 160, "PTY columns")
	flag.Parse()

	if cfg.bin == "" || cfg.logPath == "" || cfg.csvPath == "" {
		fmt.Fprintln(os.Stderr, "required: --bin --log --csv")
		os.Exit(2)
	}
	return cfg
}

// process is the PTY-driven child together with its raw-output drain.
type process struct {
	cmd     *exec.Cmd
	ptmx    *os.File
	logf    *os.File
	waitLog func() // blocks until the drain goroutine has finished
}

// startProcess launches cfg.bin under a PTY sized cfg.rows x cfg.cols and
// starts draining all PTY output into cfg.logPath.
func startProcess(cfg *driveConfig) (*process, error) {
	absBin, err := filepath.Abs(cfg.bin)
	if err != nil {
		return nil, err
	}
	logf, err := os.Create(cfg.logPath)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(absBin)
	cmd.Dir = cfg.dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(cfg.rows), Cols: uint16(cfg.cols)})
	if err != nil {
		logf.Close() // nothing else owns it yet
		return nil, fmt.Errorf("pty start: %w", err)
	}

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
	return &process{cmd: cmd, ptmx: ptmx, logf: logf, waitLog: wg.Wait}, nil
}

// close releases the PTY and the raw log; call once the child is done.
func (p *process) close() {
	p.ptmx.Close()
	p.logf.Close()
}

// sendPrompt submits prompt followed by Enter into the PTY.
func (p *process) sendPrompt(prompt string) error {
	if _, err := p.ptmx.WriteString(prompt + "\r"); err != nil {
		return fmt.Errorf("send prompt: %w", err)
	}
	fmt.Printf("prompt sent (%d chars)\n", len(prompt))
	return nil
}

// markerDeadline tracks the first sighting of the --wait-file marker so
// sampling continues for the full --grace window after it appears.
type markerDeadline struct {
	grace  time.Duration
	seenAt time.Time
}

func newMarkerDeadline(grace time.Duration) *markerDeadline {
	return &markerDeadline{grace: grace}
}

// mark records the marker's first appearance, announcing it exactly once.
func (m *markerDeadline) mark() {
	if !m.seenAt.IsZero() {
		return
	}
	m.seenAt = time.Now()
	fmt.Printf("\n  marker seen; sampling %v more\n", m.grace)
}

// expired reports whether the grace window has fully elapsed since the marker
// was first seen; always false before any sighting.
func (m *markerDeadline) expired() bool {
	return !m.seenAt.IsZero() && time.Since(m.seenAt) >= m.grace
}

// collectSamples records CPU/RSS observations at the configured cadence until
// the process exits, the fixed duration elapses (idle baseline), or the
// wait-file marker has been visible for the whole grace window.
func (p *process) collectSamples(cfg *driveConfig) []sample {
	start := time.Now()
	deadline := start.Add(cfg.duration)
	if cfg.waitFile != "" {
		deadline = start.Add(2 * time.Hour) // bounded by marker + grace
	}
	var (
		samples []sample
		markers = newMarkerDeadline(cfg.grace)
	)
	for time.Now().Before(deadline) {
		time.Sleep(cfg.sampleEvery)
		if s, ok := sampleProc(p.cmd.Process.Pid); ok {
			s.t = time.Since(start)
			samples = append(samples, s)
			fmt.Printf("\r  t=%4.0fs cpu=%5.1f%% rss=%4.0fMB", s.t.Seconds(), s.cpu, float64(s.rss)/1024)
		}
		if p.cmd.ProcessState != nil {
			break
		}
		if cfg.waitFile == "" {
			continue
		}
		if _, err := os.Stat(cfg.waitFile); err == nil {
			markers.mark()
			if markers.expired() {
				break
			}
		}
	}
	fmt.Println()
	return samples
}

// stop asks the TUI to quit politely (/quit + Enter), waits briefly for a
// clean exit and force-kills otherwise, then waits for the drain to finish.
func (p *process) stop() {
	_, _ = p.ptmx.WriteString("/quit\r")
	done := make(chan struct{})
	go func() { _ = p.cmd.Wait(); close(done) }()
	select {
	case <-done:
		fmt.Println("clean exit")
	case <-time.After(8 * time.Second):
		_ = p.cmd.Process.Kill()
		fmt.Println("killed after /quit grace")
	}
	p.waitLog()
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

func main() {
	cfg := parseFlags()
	proc, err := startProcess(&cfg)
	if err != nil {
		fatal(err)
	}
	defer proc.close()

	fmt.Printf("started pid=%d settle=%v\n", proc.cmd.Process.Pid, cfg.settle)
	time.Sleep(cfg.settle)

	if cfg.prompt != "" {
		if err := proc.sendPrompt(cfg.prompt); err != nil {
			fatal(err)
		}
	}

	samples := proc.collectSamples(&cfg)
	writeCSV(cfg.csvPath, samples)
	report(samples)
	proc.stop()
}
