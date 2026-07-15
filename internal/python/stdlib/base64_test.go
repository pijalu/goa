// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib_test

import (
	"strings"
	"testing"
)

func TestBase64Encode(t *testing.T) {
	code := `
import base64
print(base64.b64encode("hello"))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "aGVsbG8=") {
		t.Errorf("expected base64 output, got: %s", out)
	}
}

func TestBase64Decode(t *testing.T) {
	code := `
import base64
print(base64.b64decode("aGVsbG8="))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("expected hello in output, got: %s", out)
	}
}

func TestBase64URLEncode(t *testing.T) {
	code := `
import base64
print(base64.urlsafe_b64encode("a?b"))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "YT9i") {
		t.Errorf("expected URL-safe base64 output, got: %s", out)
	}
}

func TestBase64URLDecode(t *testing.T) {
	code := `
import base64
print(base64.urlsafe_b64decode("YT9i"))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "a?b") {
		t.Errorf("expected a?b in output, got: %s", out)
	}
}

func TestBase64Bytes(t *testing.T) {
	code := `
import base64
print(base64.b64encode(b"hello"))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "aGVsbG8=") {
		t.Errorf("expected base64 output, got: %s", out)
	}
}

func TestBase64Invalid(t *testing.T) {
	code := `
import base64
try:
    base64.b64decode("not!base64")
    raise ValueError("expected error")
except ValueError:
    print("OK")
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "OK") {
		t.Errorf("expected OK in output, got: %s", out)
	}
}
