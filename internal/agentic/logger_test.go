// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewLoggerWritesToStderr(t *testing.T) {
	// NewLogger should not panic and should produce a valid logger
	log := NewLogger(Trace)
	if log == nil {
		t.Fatal("NewLogger returned nil")
	}
	if log.logger == nil {
		t.Fatal("NewLogger created logger with nil underlying logger")
	}
}

func TestDefaultLogger(t *testing.T) {
	log := Default()
	if log == nil {
		t.Fatal("Default returned nil")
	}
	if log.level != Info {
		t.Errorf("Default level = %d, want Info (%d)", log.level, Info)
	}
}

func TestSetOutputRedirectsToBuffer(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(Trace)
	log.SetOutput(&buf)

	log.Log(Trace, "hello")

	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("buffer = %q, want to contain 'hello'", buf.String())
	}
}

func TestSetOutputNilDiscards(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(Trace)
	log.SetOutput(&buf)
	log.SetOutput(nil) // should set to io.Discard

	log.Log(Trace, "should not appear")

	if buf.Len() > 0 {
		t.Errorf("buffer should be empty after setting nil output, got %q", buf.String())
	}
}

func TestLoggerRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(Warn) // only Error and Warn
	log.SetOutput(&buf)

	log.Log(Debug, "debug message")
	log.Log(Info, "info message")
	log.Log(Warn, "warn message")
	log.Log(Error, "error message")

	if strings.Contains(buf.String(), "debug") {
		t.Error("debug message should not appear at Warn level")
	}
	if strings.Contains(buf.String(), "info") {
		t.Error("info message should not appear at Warn level")
	}
	if !strings.Contains(buf.String(), "warn") {
		t.Error("warn message should appear at Warn level")
	}
	if !strings.Contains(buf.String(), "error") {
		t.Error("error message should appear at Warn level")
	}
}

func TestLoggerNilReceiverNoPanic(t *testing.T) {
	var l *Logger
	// Should not panic
	l.Log(Error, "test")
}
