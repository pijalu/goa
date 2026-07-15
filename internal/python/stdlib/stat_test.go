// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib_test

import (
	"strings"
	"testing"
)

func TestStatConstants(t *testing.T) {
	code := `
import stat
print(stat.S_IFREG)
print(stat.S_IFDIR)
print(stat.S_IRUSR)
print(stat.S_IWUSR)
print(stat.S_IXUSR)
print(stat.S_IRGRP)
print(stat.S_IWGRP)
print(stat.S_IXGRP)
print(stat.S_IROTH)
print(stat.S_IWOTH)
print(stat.S_IXOTH)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"32768", "16384", "256", "128", "64", "32", "16", "8", "4", "2", "1"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

func TestStatSImode(t *testing.T) {
	code := `
import stat
mode = 0o100644
print(stat.S_IMODE(mode))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "420") { // 0o644 = 420
		t.Errorf("expected 420 in output, got: %s", out)
	}
}

func TestStatSIfmt(t *testing.T) {
	code := `
import stat
mode = 0o100644
print(stat.S_IFMT(mode))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "32768") { // 0o100000 = 32768
		t.Errorf("expected 32768 in output, got: %s", out)
	}
}

func TestStatTypeError(t *testing.T) {
	code := `
import stat
try:
    stat.S_IMODE("x")
    raise ValueError("expected error")
except TypeError:
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
