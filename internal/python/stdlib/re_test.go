// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib_test

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/go-python/gpython/compile"
	"github.com/go-python/gpython/py"
	_ "github.com/go-python/gpython/stdlib"

	// Register our stdlib modules
	_ "github.com/pijalu/goa/internal/python/stdlib"
)

// pyCode runs multi-line Python code and returns stdout + any error.
func pyCode(t *testing.T, code string) (string, error) {
	t.Helper()
	pyCtx := py.NewContext(py.DefaultContextOpts())

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe error: %v", err)
	}
	defer r.Close()

	sys := pyCtx.Store().MustGetModule("sys")
	sys.Globals["stdout"] = &py.File{File: w, FileMode: py.FileWrite}
	sys.Globals["stderr"] = &py.File{File: w, FileMode: py.FileWrite}

	var out strings.Builder
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		_, _ = io.Copy(&out, r)
	}()

	comp, err := compile.Compile(code+"\n", "<test>", py.ExecMode, 0, true)
	if err != nil {
		_ = w.Close()
		pyCtx.Close()
		<-readDone
		return "", fmt.Errorf("compile: %w", err)
	}

	_, runErr := py.RunCode(pyCtx, comp, "<test>", nil)
	_ = w.Close()
	pyCtx.Close()
	<-readDone

	return out.String(), runErr
}

// TestReCompile tests re.compile and Pattern methods.
func TestReCompile(t *testing.T) {
	code := `
import re
p = re.compile(r"[0-9]+")
print(p.pattern)
print(p.flags)
m = p.search("foo 123 bar 456")
print(m.group())
print(m.start())
print(m.end())
print(m.span())
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "123") {
		t.Errorf("expected '123' in output, got: %s", out)
	}
	if !strings.Contains(out, "[0-9]+") {
		t.Errorf("expected pattern in output, got: %s", out)
	}
}

// TestReSearch tests re.search module-level function.
func TestReSearch(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		text    string
		find    string
	}{
		{"basic digits", `[0-9]+`, "foo 123 bar", "123"},
		{"no match", `[0-9]+`, "abc def", ""},
		{"word", `\w+`, "hello world", "hello"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runReSearchTest(t, tc)
		})
	}
}

func runReSearchTest(t *testing.T, tc struct {
	name    string
	pattern string
	text    string
	find    string
}) {
	var code string
	if tc.find == "" {
		code = fmt.Sprintf(`
import re
m = re.search(r"%s", "%s")
if m is not None:
    raise ValueError("expected None")
print("OK")
`, tc.pattern, tc.text)
	} else {
		code = fmt.Sprintf(`
import re
m = re.search(r"%s", "%s")
if m is None:
    raise ValueError("expected match")
print(m.group())
`, tc.pattern, tc.text)
	}
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if tc.find != "" && !strings.Contains(out, tc.find) {
		t.Errorf("expected %q in output, got: %s", tc.find, out)
	}
	if tc.find == "" && !strings.Contains(out, "OK") {
		t.Errorf("expected OK in output, got: %s", out)
	}
}

// TestReMatch tests re.match module-level function.
func TestReMatch(t *testing.T) {
	code := `
import re
m = re.match(r"[0-9]+", "123abc")
if m is None:
    raise ValueError("expected match at start")
print(m.group())
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "123") {
		t.Errorf("expected '123' in output, got: %s", out)
	}

	// Match should fail if not at start
	code2 := `
import re
m = re.match(r"[0-9]+", "abc123")
if m is not None:
    raise ValueError("expected no match at start")
print("OK")
`
	out2, err2 := pyCode(t, code2)
	if err2 != nil {
		t.Fatalf("error: %v\noutput: %s", err2, out2)
	}
}

// TestReFindall tests re.findall module-level function.
func TestReFindall(t *testing.T) {
	code := `
import re
results = re.findall(r"[0-9]+", "a1b2c3")
for r in results:
    print(r)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "1") || !strings.Contains(out, "2") || !strings.Contains(out, "3") {
		t.Errorf("expected '1', '2', '3' in output, got: %s", out)
	}
}

// TestReSub tests re.sub module-level function.
func TestReSub(t *testing.T) {
	code := `
import re
result = re.sub(r"[0-9]+", "X", "a1b2c3")
print(result)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "aXbXcX") {
		t.Errorf("expected 'aXbXcX' in output, got: %s", out)
	}
}

// TestReSplit tests re.split module-level function.
func TestReSplit(t *testing.T) {
	code := `
import re
results = re.split(r"\s+", "a b   c d")
for r in results:
    print(r)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "a") || !strings.Contains(out, "b") || !strings.Contains(out, "c") || !strings.Contains(out, "d") {
		t.Errorf("expected 'a', 'b', 'c', 'd' in output, got: %s", out)
	}
}

// TestReEscape tests re.escape.
func TestReEscape(t *testing.T) {
	code := `
import re
result = re.escape("a.b(c)")
print(result)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, `a\.b\(c\)`) {
		t.Errorf("expected escaped pattern, got: %s", out)
	}
}

// TestReIgnoreCase tests the IGNORECASE flag.
func TestReIgnoreCase(t *testing.T) {
	code := `
import re
m = re.search(r"hello", "HELLO World", re.I)
if m is None:
    raise ValueError("expected case-insensitive match")
print(m.group())
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "HELLO") {
		t.Errorf("expected 'HELLO' in output, got: %s", out)
	}
}

// TestReMatchMethods tests Match methods: group, start, end, span.
func TestReMatchMethods(t *testing.T) {
	code := `
import re
m = re.search(r"(\w+)@(\w+)", "user@example.com")
print(m.group())
print(m.start())
print(m.end())
print(m.span())
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	// The regex (\w+)@(\w+) matches "user@example" (not the full email,
	// because \w doesn't match '.')
	if !strings.Contains(out, "user@example") {
		t.Errorf("expected match in output, got: %s", out)
	}
}

// TestRePatternMethod tests calling methods on a Pattern object.
func TestRePatternMethod(t *testing.T) {
	code := `
import re
p = re.compile(r"\d+")
m = p.search("abc 123 def")
print(m.group())
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "123") {
		t.Errorf("expected '123' in output, got: %s", out)
	}
}

// TestReNoMatch tests that None is returned for no match.
func TestReNoMatch(t *testing.T) {
	code := `
import re
m = re.search(r"\d+", "abc")
if m is not None:
    raise ValueError("expected None")
print("OK")
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
}

// TestReInvalidPattern tests error handling for invalid regex.
func TestReInvalidPattern(t *testing.T) {
	code := `
import re
try:
    p = re.compile(r"[invalid")
    raise ValueError("expected error")
except ValueError:
    print("OK")
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
}

// TestReFlagsConstant tests that re.I and re.IGNORECASE exist.
func TestReFlagsConstant(t *testing.T) {
	code := `
import re
print(re.I)
print(re.IGNORECASE)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "1") {
		t.Errorf("expected flag value, got: %s", out)
	}
}

// TestReSearchWithFlags tests the flags argument in search function.
func TestReSearchWithFlags(t *testing.T) {
	code := `
import re
m = re.search(r"hello", "HELLO World", re.IGNORECASE)
if m is None:
    raise ValueError("expected case-insensitive match")
print(m.group())
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "HELLO") {
		t.Errorf("expected 'HELLO' in output, got: %s", out)
	}
}

// TestRePatternFindall tests Pattern.findall.
func TestRePatternFindall(t *testing.T) {
	code := `
import re
p = re.compile(r"\d+")
print(p.findall("a1b2c3"))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "['1', '2', '3']") {
		t.Errorf("expected ['1', '2', '3'] in output, got: %s", out)
	}
}

// TestRePatternSub tests Pattern.sub.
func TestRePatternSub(t *testing.T) {
	code := `
import re
p = re.compile(r"\d+")
print(p.sub("X", "a1b2"))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "aXbX") {
		t.Errorf("expected aXbX in output, got: %s", out)
	}
}

// TestRePatternSplit tests Pattern.split.
func TestRePatternSplit(t *testing.T) {
	code := `
import re
p = re.compile(r"\s+")
print(p.split("a b c"))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "['a', 'b', 'c']") {
		t.Errorf("expected ['a', 'b', 'c'] in output, got: %s", out)
	}
}

// TestReMatchGroupArgs tests Match.group with argument.
func TestReMatchGroupArgs(t *testing.T) {
	code := `
import re
m = re.search(r"(\d+)-(\d+)", "12-34")
print(m.group(1))
print(m.group(0))
print(m.start(1))
print(m.end(1))
print(m.span(1))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"12", "34", "12-34", "0", "2", "0", "2"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}
