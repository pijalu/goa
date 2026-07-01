// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"net/http"
	_ "net/http/pprof" // register pprof handlers on http.DefaultServeMux
	"os"
	"path/filepath"
	"runtime/pprof"
	"sync"
	"time"
)

// Profiling support for goa, following the standard Go profiling workflow
// described in the JetBrains Go profiling guide:
//   - CPU profiling to a file via runtime/pprof (flushed on shutdown)
//   - A live net/http/pprof endpoint for `go tool pprof` attachment
//
// Enable with the --profile flag or GOA_PROFILE=1. The feature is a no-op
// unless explicitly requested, so it has zero cost in normal operation.
//
// Typical usage to diagnose the "idle 100%/17% CPU" class of bugs:
//
//	terminal A:  goa --profile
//	terminal B:  go tool pprof -http=:8080 http://127.0.0.1:6060/debug/pprof/profile?seconds=30
//
// or capture a one-shot CPU profile written on exit:
//
//	goa --profile --profile-file=./cpu.pprof
//	# (use goa, then Ctrl+C) -> inspect with: go tool pprof -http=:8080 ./cpu.pprof

const (
	defaultProfileAddr = "127.0.0.1:6060"
)

// profileOpts is the resolved configuration for a profiling session.
type profileOpts struct {
	enabled bool
	addr    string // "" disables the HTTP endpoint
	cpuFile string // "" disables the file CPU profile
}

// profileSession holds the resources started by startProfile so they can be
// stopped cleanly at shutdown (flushing the CPU profile file and closing the
// HTTP listener).
type profileSession struct {
	cpuFile *os.File
	server  *http.Server
	// announce is printed once on start so the user knows where to attach.
	announce string
}

var (
	profileMu     sync.Mutex
	activeProfile *profileSession // guarded by profileMu
)

// resolveProfileOpts merges CLI flags with environment fallbacks into a
// resolved profileOpts. Profiling is disabled unless --profile is set or
// GOA_PROFILE is "1"/"true". Empty sub-options fall back to sensible defaults.
func resolveProfileOpts(flags map[string]string, projectDir string) profileOpts {
	opts := profileOpts{}
	enabled := flags["pprof"] == "true"
	if v := os.Getenv("GOA_PROFILE"); v == "1" || v == "true" {
		enabled = true
	}
	if !enabled {
		return opts
	}
	opts.enabled = true

	opts.addr = firstNonEmpty(flags["pprof_addr"], os.Getenv("GOA_PROFILE_ADDR"), defaultProfileAddr)
	// File CPU profiling is OPT-IN: only start it when --pprof-file is set.
	// The Go runtime allows only one CPU profile at a time, so a continuous
	// file capture would block the live /debug/pprof/profile HTTP endpoint
	// (it returns 500 "cpu profiling already in use"). Default --pprof keeps
	// live attach working; pass --pprof-file for a one-shot capture-on-exit.
	opts.cpuFile = firstNonEmpty(flags["pprof_file"], os.Getenv("GOA_PROFILE_FILE"))
	return opts
}

// firstNonEmpty returns the first non-empty argument, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// startProfile enables profiling according to opts. It is safe to call once
// per process; a second call is a no-op. The returned session (also stored in
// the package-level activeProfile) must be stopped via stopProfile on shutdown.
//
// Errors are returned to the caller so a bad --profile-file path fails loudly
// rather than silently disabling profiling. The HTTP server runs in its own
// goroutine and does not block startup.
func startProfile(opts profileOpts) (*profileSession, error) {
	if !opts.enabled {
		return nil, nil
	}

	profileMu.Lock()
	defer profileMu.Unlock()
	if activeProfile != nil {
		// Already started — don't double-register pprof handlers or open a
		// second CPU profile (StartCPUProfile would return an error anyway).
		return activeProfile, nil
	}

	sess := &profileSession{}

	if opts.cpuFile != "" {
		if err := os.MkdirAll(filepath.Dir(opts.cpuFile), 0o755); err != nil {
			return nil, fmt.Errorf("profile: create dir for %s: %w", opts.cpuFile, err)
		}
		f, err := os.Create(opts.cpuFile)
		if err != nil {
			return nil, fmt.Errorf("profile: create %s: %w", opts.cpuFile, err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("profile: start CPU profile: %w", err)
		}
		sess.cpuFile = f
	}

	if opts.addr != "" {
		// net/http/pprof registers on http.DefaultServeMux via its init().
		srv := &http.Server{
			Addr:              opts.addr,
			Handler:           http.DefaultServeMux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			err := srv.ListenAndServe()
			if err != nil && err != http.ErrServerClosed {
				fmt.Fprintf(os.Stderr, "profile: http server on %s: %v\n", opts.addr, err)
			}
		}()
		sess.server = srv
	}

	sess.announce = formatAnnounce(opts)
	fmt.Fprintln(os.Stderr, sess.announce)

	activeProfile = sess
	return sess, nil
}

// formatAnnounce builds the startup banner telling the user where to attach.
func formatAnnounce(opts profileOpts) string {
	var b stringsBuilderSafe
	b.printf("goa profiling enabled:\n")
	if opts.addr != "" {
		b.printf("  live pprof:   go tool pprof http://%s/debug/pprof/profile?seconds=30\n", opts.addr)
		b.printf("  browser UI:   go tool pprof -http=:8080 http://%s/debug/pprof/\n", opts.addr)
		b.printf("  all profiles: http://%s/debug/pprof/  (goroutine, heap, block, mutex, ...)\n", opts.addr)
	}
	if opts.cpuFile != "" {
		b.printf("  cpu profile:  %s  (written on exit)\n", opts.cpuFile)
	}
	return b.String()
}

// stringsBuilderSafe is a tiny helper to avoid importing strings just for
// a formatted buffer; keeps startProfile's import list minimal.
type stringsBuilderSafe struct{ b []byte }

func (s *stringsBuilderSafe) printf(format string, args ...any) {
	s.b = append(s.b, fmt.Sprintf(format, args...)...)
}

func (s *stringsBuilderSafe) String() string { return string(s.b) }

// stopProfile stops any active profiling session: stops the CPU profile
// (flushing it to disk), closes the CPU file, and shuts down the HTTP server.
// Safe to call multiple times (idempotent via sync.Once semantics on the
// package-level activeProfile slot).
func stopProfile() {
	profileMu.Lock()
	sess := activeProfile
	activeProfile = nil
	profileMu.Unlock()
	if sess == nil {
		return
	}
	if sess.cpuFile != nil {
		pprof.StopCPUProfile()
		_ = sess.cpuFile.Close()
	}
	if sess.server != nil {
		// Close blocks until in-flight requests drain; bound it so Ctrl+C
		// shutdown is not delayed by a stuck pprof fetch.
		done := make(chan struct{})
		go func() { _ = sess.server.Close(); close(done) }()
		select {
		case <-done:
		case <-time.After(1 * time.Second):
		}
	}
}
