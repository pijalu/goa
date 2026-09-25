// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package common

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file pins the write-verification primitive (edit-tool Issue 3): a write
// tool must never report success for content that is not on disk. These tests
// exercise the primitive directly — verifying "the read-back differs" with a
// plain file the test wrote itself, rather than with permission tricks or
// filesystem races, so the detection path is deterministic.

const verifyFixture = "alpha\r\nβ — multi-byte\ngamma\n"

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("setup: write %s: %v", path, err)
	}
	return path
}

type verifiedWriteCase struct {
	name    string
	initial string
	content string
}

// TestWriteVerifyWritesExactBytes covers the happy path across the shapes an
// edit can produce: new file, overwrite, empty content, CRLF + multi-byte.
func TestWriteVerifyWritesExactBytes(t *testing.T) {
	cases := []verifiedWriteCase{
		{"creates new file", "", "one\ntwo\n"},
		{"overwrites existing content", "old content\n", "new content\n"},
		{"overwrites with shorter content", "a much longer previous body\n", "x"},
		{"writes empty content", "had content\n", ""},
		{"preserves CRLF and multi-byte runes", "seed\n", verifyFixture},
		{"writes content without trailing newline", "", "no trailing newline"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { checkVerifiedWrite(t, tc) })
	}
}

// checkVerifiedWrite writes the case content through WriteFileVerified and
// asserts the file holds exactly those bytes and that verifyWrite agrees.
func checkVerifiedWrite(t *testing.T, tc verifiedWriteCase) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "target.txt")
	if tc.initial != "" {
		if err := os.WriteFile(path, []byte(tc.initial), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := WriteFileVerified(path, []byte(tc.content), 0o600); err != nil {
		t.Fatalf("WriteFileVerified() error = %v, want nil", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != tc.content {
		t.Errorf("file content = %q, want %q", string(got), tc.content)
	}
	if err := verifyWrite(path, []byte(tc.content)); err != nil {
		t.Errorf("verifyWrite() error = %v, want nil", err)
	}
}

func TestVerifyWriteCreatesFileWithRequestedPerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.txt")
	if err := WriteFileVerified(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFileVerified() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 600", perm)
	}
}

// TestVerifyWriteOverwriteKeepsExistingPerm documents the os.WriteFile semantics
// the tool relies on: perm applies only at creation, so overwriting an edit
// target never widens/narrows the mode the user chose.
func TestVerifyWriteOverwriteKeepsExistingPerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keep.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileVerified(path, []byte("new"), 0o644); err != nil {
		t.Fatalf("WriteFileVerified() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 600 (pre-existing mode preserved)", perm)
	}
}

type verifyDivergenceCase struct {
	name    string
	onDisk  string
	want    string
	wantErr bool
}

// TestVerifyWriteDetectsDivergence is the core Issue-3 regression: the file on
// disk is not what the tool believes it wrote, and verification must say so
// instead of letting a false success through.
func TestVerifyWriteDetectsDivergence(t *testing.T) {
	cases := []verifyDivergenceCase{
		{"identical content", "same\n", "same\n", false},
		{"empty file matches empty content", "", "", false},
		{"truncated file", "partial", "partial plus more", true},
		{"different content", "new body\n", "old body\n", true},
		{"same prefix, extra tail on disk", "content\nleftover\n", "content\n", true},
		{"device-style empty read vs written bytes", "", "written\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { checkVerifyWriteDivergence(t, tc) })
	}
}

func checkVerifyWriteDivergence(t *testing.T, tc verifyDivergenceCase) {
	t.Helper()
	path := writeTemp(t, "divergence.txt", tc.onDisk)
	err := verifyWrite(path, []byte(tc.want))

	if !tc.wantErr {
		if err != nil {
			t.Fatalf("verifyWrite() error = %v, want nil", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("verifyWrite() = nil, want error (disk %q, want %q)", tc.onDisk, tc.want)
	}
	if !errors.Is(err, ErrWriteVerifyFailed) {
		t.Errorf("verifyWrite() error = %v, want errors.Is(ErrWriteVerifyFailed)", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q should name the path %q", err.Error(), path)
	}
}

// TestVerifyWriteMissingFile: a write that vanishes (or lands elsewhere and is
// removed) must be reported as a verification failure, not silently accepted.
func TestVerifyWriteMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gone.txt")
	err := verifyWrite(path, []byte("content"))
	if err == nil {
		t.Fatal("verifyWrite() = nil for a missing file, want error")
	}
	if !errors.Is(err, ErrWriteVerifyFailed) {
		t.Errorf("verifyWrite() error = %v, want errors.Is(ErrWriteVerifyFailed)", err)
	}
}

// TestVerifyWritePropagatesWriteErrors keeps the error taxonomy usable: a write
// syscall failure (missing parent directory) is NOT a verification failure, so
// callers still report write_error/permission_denied for it.
func TestVerifyWritePropagatesWriteErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent-dir", "target.txt")
	err := WriteFileVerified(path, []byte("content"), 0o644)
	if err == nil {
		t.Fatal("WriteFileVerified() = nil for a missing parent directory, want error")
	}
	if errors.Is(err, ErrWriteVerifyFailed) {
		t.Errorf("write error %v must not be reported as a verification failure", err)
	}
}

func TestVerifyWriteNilContentWritesEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nil.txt")
	if err := WriteFileVerified(path, nil, 0o644); err != nil {
		t.Fatalf("WriteFileVerified(nil) error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Errorf("size = %d, want 0", info.Size())
	}
}
