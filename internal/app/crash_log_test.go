// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal"
)

// These tests mutate process-wide state (log output, the crashLogFile global,
// and — on unix — fd 2). They must NOT run in parallel.

func TestCrashLogPath(t *testing.T) {
	t.Run("env override wins", func(t *testing.T) {
		t.Setenv("GOA_CRASH_LOG", "/tmp/custom-goa-crash.log")
		if got := crashLogPath("/some/project"); got != "/tmp/custom-goa-crash.log" {
			t.Fatalf("crashLogPath = %q, want env override", got)
		}
	})
	t.Run("project dir", func(t *testing.T) {
		t.Setenv("GOA_CRASH_LOG", "")
		want := filepath.Join("/some/project", ".goa", "crash.log")
		if got := crashLogPath("/some/project"); got != want {
			t.Fatalf("crashLogPath = %q, want %q", got, want)
		}
	})
	t.Run("home fallback", func(t *testing.T) {
		t.Setenv("GOA_CRASH_LOG", "")
		// crashLogPath resolves the home via internal.GoaHome() (which honors
		// GOA_HOME / the package TestMain scratch home), NOT os.UserHomeDir() —
		// so compute the expectation from the same source.
		home, ok := internal.GoaHome()
		if !ok {
			t.Skip("no home dir")
		}
		want := filepath.Join(home, ".goa", "crash.log")
		if got := crashLogPath(""); got != want {
			t.Fatalf("crashLogPath = %q, want %q", got, want)
		}
	})
}

func TestSetupCrashLog_CapturesLogOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crash.log")
	t.Setenv("GOA_CRASH_LOG", path)

	origLog := log.Writer()
	cleanup := setupCrashLog("")
	log.Print("crash-log-marker-12345")
	cleanup()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read crash log: %v", err)
	}
	if !strings.Contains(string(data), "crash-log-marker-12345") {
		t.Fatalf("crash log missing log output:\n%s", data)
	}
	if !strings.Contains(string(data), "goa crash log started") {
		t.Fatalf("crash log missing startup header:\n%s", data)
	}
	if crashLogFile != nil {
		t.Fatal("crashLogFile not reset after cleanup")
	}
	if log.Writer() != origLog {
		t.Fatal("log output not restored after cleanup")
	}
}

func TestWriteCrashLog(t *testing.T) {
	t.Run("nil file is a no-op", func(t *testing.T) {
		crashLogFile = nil
		writeCrashLog("boom", []byte("stack")) // must not panic
	})
	t.Run("persists panic and stack", func(t *testing.T) {
		f, err := os.Create(filepath.Join(t.TempDir(), "crash.log"))
		if err != nil {
			t.Fatal(err)
		}
		crashLogFile = f
		defer func() { crashLogFile = nil }()

		writeCrashLog("boom-value", []byte("goroutine 1 [running]:..."))
		_ = f.Close()

		data, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "Panic: boom-value") {
			t.Fatalf("crash log missing panic value:\n%s", data)
		}
		if !strings.Contains(string(data), "goroutine 1 [running]") {
			t.Fatalf("crash log missing stack:\n%s", data)
		}
	})
}
