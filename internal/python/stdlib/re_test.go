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

	"github.com/pijalu/gpython/compile"
	"github.com/pijalu/gpython/py"
	_ "github.com/pijalu/gpython/stdlib"

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

// TestReFlagsDotallMultilineVerbose is the regression test for the session
// failure (2026-08-02 export): re.DOTALL / re.S / re.M / re.X raised
// AttributeError: 'module' has no attribute 'DOTALL'.
func TestReFlagsDotallMultilineVerbose(t *testing.T) {
	code := `
import re

# 1. flag constants exist (was AttributeError)
print("flags:", re.DOTALL == re.S, re.MULTILINE == re.M, re.VERBOSE == re.X)

# 2. DOTALL: dot matches newline
m = re.search(r'a.b', 'a\nb', re.DOTALL)
print("dotall:", m is not None and m.group(0) == 'a\nb')
m2 = re.search(r'a.b', 'a\nb')
print("nodotall:", m2 is None)

# 3. MULTILINE: ^ matches after newline
f = re.findall(r'^b', 'a\nb', re.MULTILINE)
print("multiline:", f == ['b'])

# 4. combined S | I
m3 = re.search(r'A.B', 'a\nb', re.S | re.I)
print("combined:", m3 is not None)

# 5. VERBOSE: whitespace and comments stripped outside classes
p = re.compile(r"""
    var\ yyRuleInfoNRhs  # the rule table
    \ =\ \[\]int\{       # literal assignment
    (.*?)                # capture body
    \n\}
""", re.VERBOSE)
m4 = p.search('var yyRuleInfoNRhs = []int{1, 2,\n}')
print("verbose:", m4 is not None and m4.group(1) == '1, 2,')

# 5b. whitespace and # are kept inside character classes
m5 = re.search(r'[ #]+', ' #x', re.X)
print("verbose_class:", m5 is not None and m5.group(0) == ' #')

# 5c. escaped space is kept
m6 = re.search(r'a\ b', 'a b', re.X)
print("verbose_escaped:", m6 is not None)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{
		"flags: True", "dotall: True", "nodotall: True", "multiline: True",
		"combined: True", "verbose: True", "verbose_class: True", "verbose_escaped: True",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

// TestReFinditer covers module-level re.finditer: Match objects with correct
// group(0)/start()/end() for a multi-match pattern, the empty case, flags,
// and iteration/list() usage (bugs.md re.finditer parity gap).
func TestReFinditer(t *testing.T) {
	code := `
import re

# 3-match pattern: Match objects with correct group(0), start(), end()
ms = re.finditer(r'\d+', 'a1 b22 c333')
print("count:", len(ms))
for m in ms:
    print(m.group(0), m.start(), m.end())

# group(n) on a finditer match
ms2 = re.finditer(r'(\w+)=(\d+)', 'x=1 y=2')
for m in ms2:
    print(m.group(1), m.group(2))

# empty case returns []
print("empty:", re.finditer(r'z+', 'abc'))

# flags combine: case-insensitive
print("flags:", len(re.finditer(r'a', 'A a', re.I)))

# list() usage works
print("list:", list(re.finditer(r'\d', '1 2')) is not None)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{
		"count: 3", "1 1 2", "22 4 6", "333 8 11",
		"x 1", "y 2", "empty: []", "flags: 2", "list: True",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

// TestReFinditer_PatternMethod covers Pattern.finditer parity with the
// module-level function.
func TestReFinditer_PatternMethod(t *testing.T) {
	code := `
import re
p = re.compile(r'\d+')
ms = p.finditer('a1 b22 c333')
print("count:", len(ms))
for m in ms:
    print(m.group(0), m.start(), m.end())
print("empty:", p.finditer('abc'))
print("string_attr:", ms[0].string)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{
		"count: 3", "1 1 2", "22 4 6", "333 8 11",
		"empty: []", "string_attr: a1 b22 c333",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

// TestReSubCallable replays the reported session failure: re.sub with a
// callable replacement raised "TypeError: 'sub() argument must be str, not
// function'". Callable repl must receive a Match and its return value is
// spliced in per match.
func TestReSubCallable(t *testing.T) {
	code := `
import re

def sub_se(m):
    return "skillEnabled(%s, %s, nil)" % (m.group(1), m.group(2))

s2 = re.sub(r'skillEnabled\((cfg|got|fresh), (["a-zA-Z0-9_-]+|name)\)', sub_se,
            'skillEnabled(cfg, "goal") and skillEnabled(got, name)')
print(s2)
print(re.sub(r'[0-9]+', lambda m: str(int(m.group(0)) * 2), 'a1 b22 c333'))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{
		`skillEnabled(cfg, "goal", nil) and skillEnabled(got, name, nil)`,
		"a2 b44 c666",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

// TestReSubCallablePattern covers Pattern.sub with a callable replacement.
func TestReSubCallablePattern(t *testing.T) {
	code := `
import re
p = re.compile(r'(a)(b)?')
print(p.sub(lambda m: '<' + m.group(1) + '>', 'ab a ab'))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "<a> <a> <a>") {
		t.Errorf("expected '<a> <a> <a>' in output, got: %s", out)
	}
}

// TestReSubCallableNoMatch verifies the callback is never invoked when the
// pattern does not match.
func TestReSubCallableNoMatch(t *testing.T) {
	code := `
import re
calls = []
def cb(m):
    calls.append(m.group(0))
    return 'X'
print(re.sub(r'z+', cb, 'abc'))
print('calls:', len(calls))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "abc") || !strings.Contains(out, "calls: 0") {
		t.Errorf("expected unchanged text and 0 calls, got: %s", out)
	}
}

// TestReSubCallableException verifies an exception raised by the callback
// propagates unchanged.
func TestReSubCallableException(t *testing.T) {
	code := `
import re
def boom(m):
    raise ValueError('from callback')
print(re.sub(r'a', boom, 'a'))
`
	out, err := pyCode(t, code)
	if err == nil {
		t.Fatalf("expected callback exception, got output: %s", out)
	}
	if !strings.Contains(err.Error(), "ValueError") || !strings.Contains(err.Error(), "from callback") {
		t.Errorf("expected ValueError 'from callback' to propagate, got: %v", err)
	}
}

// TestReSubTemplate covers CPython-style replacement templates: \1 numeric
// references, \g<N>, \g<name>, literal backslash handling, and error cases.
func TestReSubTemplate(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{
			"numeric reference",
			`import re
print(re.sub(r'(\w+)@(\w+)', r'\2@\1', 'user@host'))`,
			"host@user",
		},
		{
			"g numeric reference",
			`import re
print(re.sub(r'(\w+)@(\w+)', r'\g<2>.\g<1>', 'user@host'))`,
			"host.user",
		},
		{
			"g named reference",
			`import re
print(re.sub(r'(?P<word>\w+)=(?P<val>\w+)', r'\g<val>:\g<word>', 'a=1'))`,
			"1:a",
		},
		{
			"group zero whole match",
			`import re
print(re.sub(r'\d+', r'[\g<0>]', 'a12b'))`,
			"a[12]b",
		},
		{
			"escaped backslash",
			`import re
print(re.sub(r'a', r'\\path\\', 'a a'))`,
			`\path\ \path\`,
		},
		{
			"literal backslash-n kept",
			`import re
print(re.sub(r'a', r'\\n', 'a'))`,
			`\n`,
		},
		{
			"mixed literal and reference",
			`import re
print(re.sub(r'x(\d)x', r'<\1>!', 'x1x x2x'))`,
			"<1>! <2>!",
		},
		{
			"no match returns original",
			`import re
print(re.sub(r'z(\d)', r'\1!', 'abc'))`,
			"abc",
		},
		{
			"pattern method template",
			`import re
p = re.compile(r'(\w)(\w)')
print(p.sub(r'\2\1', 'ab cd'))`,
			"ba dc",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := pyCode(t, tt.code)
			if err != nil {
				t.Fatalf("error: %v\noutput: %s", err, out)
			}
			if !strings.Contains(out, tt.want) {
				t.Errorf("expected %q in output, got: %s", tt.want, out)
			}
		})
	}
}

// TestReSubTemplateErrors covers template error cases: out-of-range group,
// bad escape, unknown group name, and non-string non-callable repl.
func TestReSubTemplateErrors(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{
			"out of range group",
			`import re
print(re.sub(r'(a)', r'\2', 'a'))`,
			"invalid group reference",
		},
		{
			"bad escape letter",
			`import re
print(re.sub(r'(a)', r'\q', 'a'))`,
			"bad escape",
		},
		{
			"unknown group name",
			`import re
print(re.sub(r'(a)', r'\g<nope>', 'a'))`,
			"unknown group name",
		},
		{
			"int repl rejected",
			`import re
print(re.sub(r'a', 42, 'a'))`,
			"TypeError",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := pyCode(t, tt.code)
			if err == nil {
				t.Fatalf("expected error, got output: %s", out)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("expected error containing %q, got: %v", tt.want, err)
			}
		})
	}
}
