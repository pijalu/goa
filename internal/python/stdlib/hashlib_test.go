// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib_test

import (
	"strings"
	"testing"
)

func TestHashlibMD5(t *testing.T) {
	code := `
import hashlib
h = hashlib.md5("hello")
print(h.hexdigest())
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "5d41402abc4b2a76b9719d911017c592") {
		t.Errorf("expected MD5 hex digest in output, got: %s", out)
	}
}

func TestHashlibSHA256(t *testing.T) {
	code := `
import hashlib
h = hashlib.sha256("hello")
print(len(h.hexdigest()))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "64") {
		t.Errorf("expected SHA256 hex digest length 64 in output, got: %s", out)
	}
}

func TestHashlibDigest(t *testing.T) {
	code := `
import hashlib
h = hashlib.md5("hello")
print(repr(h.digest()))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "b'") {
		t.Errorf("expected bytes repr in output, got: %s", out)
	}
}

func TestHashlibSHA1(t *testing.T) {
	code := `
import hashlib
h = hashlib.sha1("hello")
print(len(h.hexdigest()))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "40") {
		t.Errorf("expected SHA1 hex digest length 40 in output, got: %s", out)
	}
}

func TestHashlibSHA512(t *testing.T) {
	code := `
import hashlib
h = hashlib.sha512("hello")
print(len(h.hexdigest()))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "128") {
		t.Errorf("expected SHA512 hex digest length 128 in output, got: %s", out)
	}
}

func TestHashlibBytes(t *testing.T) {
	code := `
import hashlib
h = hashlib.md5(b"hello")
print(h.hexdigest())
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "5d41402abc4b2a76b9719d911017c592") {
		t.Errorf("expected MD5 hex digest in output, got: %s", out)
	}
}
