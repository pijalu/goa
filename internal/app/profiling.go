// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
)

// profileState holds open profiling sinks so they can be flushed when the
// application exits. Profiling is opt-in via the --cpuprofile, --memprofile,
// and --trace flags.
type profileState struct {
	cpuPath   string
	memPath   string
	tracePath string
	cpuFile   *os.File
	traceFile *os.File
}

// startProfiling opens the requested profiles and begins collection.
// It returns an error if any requested profile cannot be started; in that
// case no collectors are left running.
// startProfiling opens the requested profiles and begins collection.
// It returns an error if any requested profile cannot be started; in that
// case no collectors are left running.
func startProfiling(opts RuntimeOptions) (_ *profileState, err error) {
	p := &profileState{cpuPath: opts.CPUProfile, memPath: opts.MemProfile, tracePath: opts.TraceFile}
	defer func() {
		if err != nil {
			p.stopProfiling()
		}
	}()

	if opts.CPUProfile != "" {
		f, err := os.Create(opts.CPUProfile)
		if err != nil {
			return nil, fmt.Errorf("create cpu profile: %w", err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			return nil, fmt.Errorf("start cpu profile: %w", err)
		}
		p.cpuFile = f
	}

	if opts.TraceFile != "" {
		f, err := os.Create(opts.TraceFile)
		if err != nil {
			return nil, fmt.Errorf("create trace: %w", err)
		}
		if err := trace.Start(f); err != nil {
			return nil, fmt.Errorf("start trace: %w", err)
		}
		p.traceFile = f
	}

	return p, nil
}

// stopProfiling flushes all requested profiles. It is safe to call even when
// profiling was not started.
func (p *profileState) stopProfiling() {
	if p == nil {
		return
	}
	if p.cpuFile != nil {
		pprof.StopCPUProfile()
		p.cpuFile.Close()
		p.cpuFile = nil
	}
	if p.traceFile != nil {
		trace.Stop()
		p.traceFile.Close()
		p.traceFile = nil
	}
	if p.memPath != "" {
		writeMemProfile(p.memPath)
	}
}

func writeMemProfile(path string) {
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: create mem profile: %v\n", err)
		return
	}
	defer f.Close()
	runtime.GC()
	if err := pprof.WriteHeapProfile(f); err != nil {
		fmt.Fprintf(os.Stderr, "Error: write heap profile: %v\n", err)
	}
}
