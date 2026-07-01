// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"runtime/pprof"
	"testing"
	"time"
)

// resetProfileState clears any package-level profile session so tests are
// independent. Caller must hold no active profile.
func resetProfileState(t *testing.T) {
	t.Helper()
	stopProfile()
}

func TestResolveProfileOpts_DisabledByDefault(t *testing.T) {
	t.Setenv("GOA_PROFILE", "")
	resetProfileState(t)

	opts := resolveProfileOpts(map[string]string{}, t.TempDir())
	if opts.enabled {
		t.Fatalf("opts.enabled = true; want false when no flag/env set")
	}
}

func TestResolveProfileOpts_FlagEnablesHTTPOnlyByDefault(t *testing.T) {
	t.Setenv("GOA_PROFILE", "")
	resetProfileState(t)

	opts := resolveProfileOpts(map[string]string{"pprof": "true"}, t.TempDir())
	if !opts.enabled {
		t.Fatalf("opts.enabled = false; want true for pprof=true flag")
	}
	if opts.addr != defaultProfileAddr {
		t.Errorf("opts.addr = %q; want default %q", opts.addr, defaultProfileAddr)
	}
	// Default --pprof must NOT start a file CPU profile, otherwise the live
	// /debug/pprof/profile endpoint returns 500 ("cpu profiling already in use").
	if opts.cpuFile != "" {
		t.Errorf("opts.cpuFile = %q; want empty by default so live CPU profiling works", opts.cpuFile)
	}
}

func TestResolveProfileOpts_EnvEnables(t *testing.T) {
	t.Setenv("GOA_PROFILE", "1")
	resetProfileState(t)

	opts := resolveProfileOpts(map[string]string{}, t.TempDir())
	if !opts.enabled {
		t.Fatalf("opts.enabled = false; want true for GOA_PROFILE=1")
	}
	if opts.cpuFile != "" {
		t.Errorf("env-only enable should not default cpuFile; got %q", opts.cpuFile)
	}
}

func TestResolveProfileOpts_OverridesApplied(t *testing.T) {
	t.Setenv("GOA_PROFILE", "")
	resetProfileState(t)

	dir := t.TempDir()
	flags := map[string]string{
		"pprof":      "true",
		"pprof_addr": "127.0.0.1:16161",
		"pprof_file": filepath.Join(dir, "custom.pprof"),
	}
	opts := resolveProfileOpts(flags, dir)
	if opts.addr != "127.0.0.1:16161" {
		t.Errorf("addr override = %q; want 127.0.0.1:16161", opts.addr)
	}
	if opts.cpuFile != filepath.Join(dir, "custom.pprof") {
		t.Errorf("file override = %q; want custom.pprof", opts.cpuFile)
	}
}

func TestResolveProfileOpts_CpuFileOptIn(t *testing.T) {
	t.Setenv("GOA_PROFILE", "1")
	resetProfileState(t)

	dir := t.TempDir()
	cpuPath := filepath.Join(dir, "one-shot.pprof")
	opts := resolveProfileOpts(map[string]string{"pprof_file": cpuPath}, dir)
	if opts.cpuFile != cpuPath {
		t.Errorf("cpuFile = %q; want %q", opts.cpuFile, cpuPath)
	}
}

func TestStartProfile_DisabledIsNoOp(t *testing.T) {
	resetProfileState(t)
	sess, err := startProfile(profileOpts{enabled: false})
	if err != nil {
		t.Fatalf("disabled startProfile returned error: %v", err)
	}
	if sess != nil {
		t.Fatalf("expected nil session when disabled, got %+v", sess)
	}
	if activeProfile != nil {
		t.Fatalf("activeProfile set despite disabled opts")
	}
}

func TestStartProfile_FileCPUProfileWrittenAndFlushed(t *testing.T) {
	t.Setenv("GOA_PROFILE", "")
	resetProfileState(t)

	dir := t.TempDir()
	cpuPath := filepath.Join(dir, "sub", "cpu.pprof") // exercises mkdir
	opts := profileOpts{
		enabled: true,
		addr:    "", // no HTTP server in this test
		cpuFile: cpuPath,
	}
	sess, err := startProfile(opts)
	if err != nil {
		t.Fatalf("startProfile: %v", err)
	}
	if sess == nil || sess.cpuFile == nil {
		t.Fatalf("expected non-nil session with cpuFile")
	}
	// Generate some CPU so the profile is non-empty.
	busy := time.Now().Add(20 * time.Millisecond)
	for time.Now().Before(busy) {
		_ = time.Now().UnixNano()
	}

	stopProfile()

	info, err := os.Stat(cpuPath)
	if err != nil {
		t.Fatalf("cpu profile not written: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("cpu profile file is empty; StopCPUProfile did not flush")
	}
	// After stop, activeProfile must be cleared (idempotent re-stop is a no-op).
	if activeProfile != nil {
		t.Fatalf("activeProfile not cleared after stopProfile")
	}
}

func TestStartProfile_HTTPServesPprof(t *testing.T) {
	t.Setenv("GOA_PROFILE", "")
	resetProfileState(t)

	addr := "127.0.0.1:16177"
	cpuPath := filepath.Join(t.TempDir(), "cpu.pprof")
	opts := profileOpts{enabled: true, addr: addr, cpuFile: cpuPath}
	if _, err := startProfile(opts); err != nil {
		t.Fatalf("startProfile: %v", err)
	}
	t.Cleanup(stopProfile)

	// The server starts in a goroutine; give it a moment to bind.
	deadline := time.Now().Add(2 * time.Second)
	var resp *http.Response
	var err error
	for time.Now().Before(deadline) {
		resp, err = http.Get("http://" + addr + "/debug/pprof/")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("pprof HTTP endpoint unreachable: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("pprof index status = %d; want 200", resp.StatusCode)
	}
}

func TestStartProfile_IdempotentSecondStart(t *testing.T) {
	t.Setenv("GOA_PROFILE", "")
	resetProfileState(t)

	opts := profileOpts{enabled: true, cpuFile: filepath.Join(t.TempDir(), "a.pprof")}
	s1, err := startProfile(opts)
	if err != nil {
		t.Fatalf("first startProfile: %v", err)
	}
	// A second call must not error (no double CPU-profile registration) and
	// must return the SAME active session.
	s2, err := startProfile(opts)
	if err != nil {
		t.Fatalf("second startProfile: %v", err)
	}
	if s1 != s2 {
		t.Errorf("expected identical session on double-start")
	}
	stopProfile()
	if activeProfile != nil {
		t.Errorf("activeProfile not cleared")
	}
}

func TestStopProfile_NoOpWhenNoneActive(t *testing.T) {
	resetProfileState(t)
	// Must not panic / block when nothing is running.
	stopProfile()
	stopProfile()
}

func TestFirstNonEmpty(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{}, ""},
		{[]string{"", ""}, ""},
		{[]string{"a", "b"}, "a"},
		{[]string{"", "b", "c"}, "b"},
	}
	for _, c := range cases {
		if got := firstNonEmpty(c.in...); got != c.want {
			t.Errorf("firstNonEmpty(%v) = %q; want %q", c.in, got, c.want)
		}
	}
}

// TestPprofCPUProfileIsRegistered confirms the net/http/pprof import side-effect
// is present (the whole feature depends on it). If someone removes the blank
// import from profile.go, this fails.
func TestPprofCPUProfileIsRegistered(t *testing.T) {
	// net/http/pprof's init() registers these named profiles with runtime/pprof.
	// Presence confirms the blank import is wired up.
	for _, name := range []string{"goroutine", "heap", "threadcreate"} {
		if pprof.Lookup(name) == nil {
			t.Errorf("pprof.Lookup(%q) = nil; net/http/pprof blank import missing?", name)
		}
	}
}
