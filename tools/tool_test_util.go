// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import "os"

// tempDir creates a temporary test directory.
func tempDir(t testingT) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "goa-tool-test-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	return dir, func() { os.RemoveAll(dir) }
}

// writeFile writes content to a file.
func writeFile(t testingT, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// testingT is a subset of testing.TB for use in helper functions.
type testingT interface {
	Helper()
	Fatalf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}
