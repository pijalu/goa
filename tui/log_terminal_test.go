// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type logTermMock struct {
	buf bytes.Buffer
}

func (m *logTermMock) Start(onInput func(string), onResize func()) {}
func (m *logTermMock) Stop()                                       {}
func (m *logTermMock) Write(p []byte) (int, error)                 { return m.buf.Write(p) }
func (m *logTermMock) WriteString(s string)                        { m.buf.WriteString(s) }
func (m *logTermMock) Size() (width, height int)                   { return 80, 24 }
func (m *logTermMock) SetRaw() (restore func(), err error)         { return func() {}, nil }
func (m *logTermMock) HideCursor()                                 {}
func (m *logTermMock) ShowCursor()                                 {}
func (m *logTermMock) ClearScreen()                                {}
func (m *logTermMock) SetTitle(title string)                      {}

func TestLogTerminal(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "term.log")

	inner := &logTermMock{}
	lt, err := NewLogTerminal(inner, logPath)
	if err != nil {
		t.Fatalf("NewLogTerminal: %v", err)
	}
	defer lt.Stop()

	lt.WriteString("hello")
	if _, err := lt.Write([]byte(" world")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	lt.Stop()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !bytes.Contains(data, []byte("hello")) {
		t.Errorf("log missing hello: %q", data)
	}
	if !bytes.Contains(data, []byte(" world")) {
		t.Errorf("log missing world: %q", data)
	}
	if strings.Count(string(data), "write") != 2 {
		t.Errorf("expected 2 write headers, got %d: %q", strings.Count(string(data), "write"), data)
	}
}
