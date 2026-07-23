// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPythonTool_Schema(t *testing.T) {
	tool := &PythonTool{}
	s := tool.Schema()
	if s.Name != "python" {
		t.Errorf("Name = %q, want %q", s.Name, "python")
	}
	props, ok := s.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("missing properties")
	}
	if _, ok := props["code"]; !ok {
		t.Error("missing code property")
	}
	if _, ok := props["timeout"]; !ok {
		t.Error("missing timeout property")
	}
}

func TestPythonTool_Execute_Print(t *testing.T) {
	tool := &PythonTool{}
	out, err := tool.Execute(`{"code": "print('hello world')"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(out, "hello world") {
		t.Errorf("output = %q, want hello world", out)
	}
}

func TestPythonTool_Execute_Computation(t *testing.T) {
	tool := &PythonTool{}
	out, err := tool.Execute(`{"code": "print(sum(range(10)))"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(out, "45") {
		t.Errorf("output = %q, want 45", out)
	}
}

func TestPythonTool_Execute_MultilineFunction(t *testing.T) {
	tool := &PythonTool{}
	code := `def add(a, b):
    return a + b

result = add(2, 3)
print("Result:", result)
`
	out, err := tool.Execute(fmt.Sprintf(`{"code": %q}`, code))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(out, "Result: 5") {
		t.Errorf("output = %q, want Result: 5", out)
	}
}

func TestPythonTool_Execute_FString(t *testing.T) {
	tool := &PythonTool{}
	out, err := tool.Execute(`{"code": "result = 5 + 7\nprint(f\"The sum is: {result}\")"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(out, "The sum is: 12") {
		t.Errorf("output = %q, want The sum is: 12", out)
	}
}

func TestPythonTool_Execute_FStringMultipleExpressions(t *testing.T) {
	tool := &PythonTool{}
	out, err := tool.Execute(`{"code": "a = 3\nb = 4\nprint(f\"a={a}, b={b}, sum={a+b}\")"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(out, "a=3, b=4, sum=7") {
		t.Errorf("output = %q, want a=3, b=4, sum=7", out)
	}
}

func TestPythonTool_Execute_FStringFormatSpec(t *testing.T) {
	tool := &PythonTool{}
	out, err := tool.Execute(`{"code": "pi = 3.14159\nprint(f\"pi = {pi:.2f}\")"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(out, "pi = 3.14") {
		t.Errorf("output = %q, want pi = 3.14", out)
	}
}

func TestPythonTool_transformFStrings(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{
			`print(f"The sum is: {result}")`,
			`print("The sum is: %s" % (result))`,
		},
		{
			`x = f"a={a}, b={b}"`,
			`x = "a=%s, b=%s" % (a, b)`,
		},
		{
			`print(f"pi = {pi:.2f}")`,
			`print("pi = %.2f" % (pi))`,
		},
		{
			`print('hello')`,
			`print('hello')`,
		},
	}
	for _, tc := range tests {
		got := transformFStrings(tc.in)
		if got != tc.want {
			t.Errorf("transformFStrings(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPythonTool_Execute_StdlibModules(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string
	}{
		{
			name: "re",
			code: `import re; m = re.search(r"[0-9]+", "foo 123 bar"); print(m.group())`,
			want: "123",
		},
		{
			name: "json",
			code: `import json; print(json.loads('{"a":1}')["a"])`,
			want: "1",
		},
		{
			name: "datetime",
			code: `import datetime; dt = datetime.datetime(2024, 1, 1, 12, 0, 0); print(dt.strftime("%Y-%m-%d"))`,
			want: "2024-01-01",
		},
		{
			name: "random",
			code: `import random; random.seed(7); r = random.randint(1, 10); print(1 <= r <= 10)`,
			want: "True",
		},
		{
			name: "hashlib",
			code: `import hashlib; print(hashlib.md5("hello").hexdigest())`,
			want: "5d41402abc4b2a76b9719d911017c592",
		},
		{
			name: "base64",
			code: `import base64; print(base64.b64encode("hello"))`,
			want: "aGVsbG8=",
		},
		{
			name: "urllib.parse",
			code: `import urllib.parse; print(urllib.parse.quote("a b"))`,
			want: "a%20b",
		},
		{
			name: "collections",
			code: `import collections; c = collections.Counter("abba"); print(c["a"])`,
			want: "2",
		},
		{
			name: "itertools",
			code: `import itertools; print(list(itertools.combinations("abc", 2)))`,
			want: "[['a', 'b'], ['a', 'c'], ['b', 'c']]",
		},
		{
			name: "stat",
			code: `import stat; print(stat.S_IRUSR)`,
			want: "256",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool := &PythonTool{}
			out, err := tool.Execute(fmt.Sprintf(`{"code": %q}`, tc.code))
			if err != nil {
				t.Fatalf("Execute failed: %v\noutput: %s", err, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output = %q, want %q", out, tc.want)
			}
		})
	}
}

func TestPythonTool_Execute_RuntimeError(t *testing.T) {
	tool := &PythonTool{}
	_, err := tool.Execute(`{"code": "1/0"}`)
	if err == nil {
		t.Fatal("expected error for division by zero")
	}
	if !strings.Contains(err.Error(), "ZeroDivisionError") {
		t.Errorf("error = %q, want ZeroDivisionError", err.Error())
	}
}

func TestPythonTool_Execute_InvalidInput(t *testing.T) {
	tool := &PythonTool{}
	_, err := tool.Execute(`not json`)
	if err == nil {
		t.Fatal("expected error for invalid input")
	}
}

func TestPythonTool_Execute_MissingCode(t *testing.T) {
	tool := &PythonTool{}
	_, err := tool.Execute(`{"timeout": 30}`)
	if err == nil {
		t.Fatal("expected error for missing code")
	}
}

func TestPythonTool_Execute_Timeout(t *testing.T) {
	tool := &PythonTool{}
	// A tight loop in gpython should hit the short timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := tool.ExecuteContext(ctx, `{"code": "while True: pass", "timeout": 1}`)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") && !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error = %q, want timeout or cancellation", err.Error())
	}
}

func TestPythonTool_Execute_Cancellation(t *testing.T) {
	tool := &PythonTool{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tool.ExecuteContext(ctx, `{"code": "print('hello')"}`)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error = %q, want cancelled", err.Error())
	}
}

func TestPythonTool_ShortDoc(t *testing.T) {
	tool := &PythonTool{}
	if tool.ShortDoc() == "" {
		t.Error("ShortDoc should not be empty")
	}
}

func TestPythonTool_LongDoc(t *testing.T) {
	tool := &PythonTool{}
	if tool.LongDoc() == "" {
		t.Error("LongDoc should not be empty")
	}
}

func TestPythonTool_Examples(t *testing.T) {
	tool := &PythonTool{}
	if len(tool.Examples()) == 0 {
		t.Error("Examples should not be empty")
	}
}

func TestPythonTool_LoopHints(t *testing.T) {
	tool := &PythonTool{}
	hints := tool.LoopHints()
	if hints.HealArg != "code" {
		t.Errorf("HealArg = %q, want code", hints.HealArg)
	}
	if hints.Status == nil {
		t.Fatal("Status should not be nil")
	}
	status := hints.Status(`{"code": "print(1)"}`)
	if !strings.Contains(status, "print(1)") {
		t.Errorf("Status = %q, want print(1)", status)
	}
}

func TestPythonTool_Access(t *testing.T) {
	tool := &PythonTool{}
	access := tool.Access("")
	if access.Category != "shell" {
		t.Errorf("Category = %q, want shell", access.Category)
	}
}

func TestPythonTool_NormalizeTimeout(t *testing.T) {
	tests := []struct {
		input    int
		fallback int
		want     int
	}{
		{0, 0, DefaultPythonTimeoutS},
		{30, 0, 30},
		{-1, 90, 90},
		{MaxPythonTimeoutS + 1, 0, MaxPythonTimeoutS},
	}
	for _, tt := range tests {
		got := normalizePythonTimeout(tt.input, tt.fallback)
		if got != tt.want {
			t.Errorf("normalizePythonTimeout(%d, %d) = %d, want %d", tt.input, tt.fallback, got, tt.want)
		}
	}
}

func TestPythonTool_TruncateOutput(t *testing.T) {
	tool := &PythonTool{MaxOutputLines: 2}
	out := tool.truncateOutput("line1\nline2\nline3\n")
	if strings.Contains(out, "line1") {
		t.Error("expected earlier lines to be truncated")
	}
	if !strings.Contains(out, "line3") {
		t.Error("expected last line to be kept")
	}
	if !strings.Contains(out, "truncated") {
		t.Error("expected truncation notice")
	}
}

// TestPythonTool_Execute_SanitizesControlBytes: script output is untrusted —
// raw ESC bytes must become visible text before reaching the model/TUI.
func TestPythonTool_Execute_SanitizesControlBytes(t *testing.T) {
	tool := &PythonTool{}
	out, err := tool.Execute(`{"code": "print('\\x1b[2Kwiped')"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("raw ESC byte leaked into tool output: %q", out)
	}
	if !strings.Contains(out, `\e[2Kwiped`) {
		t.Errorf("expected literal escape text, got: %q", out)
	}
}

// TestPythonTool_ForkFixes verifies the pijalu/gpython fork patches fix the
// four failure classes observed in Goa session logs (bytes subscription,
// file iteration, tuple ordering, open encoding kwarg). Each case replays a
// functionally equivalent script through the jail-enabled PythonTool.
func TestPythonTool_ForkFixes(t *testing.T) {
	dir := t.TempDir()

	// 1. 'bytes' object is not subscriptable — open(rb).read() slicing/indexing
	tool := &PythonTool{ProjectDir: dir, Jail: true}
	out, err := tool.Execute(`{"code": "f = open('data.bin', 'wb')\nf.write(b'hello world payload')\nf.close()\nsrc = open('data.bin', 'rb').read()\nprint(src[:5])\nprint(src[0])\nprint(len(src))\ncount = 0\nfor b in src:\n    if b == 111:\n        count += 1\nprint(count)"}`)
	if err != nil {
		t.Fatalf("bytes subscript case failed: %v", err)
	}
	for _, want := range []string{"b'hello", "104", "19", "3"} {
		if !strings.Contains(out, want) {
			t.Errorf("bytes case missing %q, got:\n%s", want, out)
		}
	}

	// 2. 'file' object is not iterable — for line in f
	out, err = tool.Execute(`{"code": "f = open('data.bin', 'w')\nf.write('line one\\nline two\\nline three\\n')\nf.close()\nlines = []\nwith open('data.bin') as f:\n    for line in f:\n        lines.append(line.strip())\nprint(lines)\nprint(len(lines))"}`)
	if err != nil {
		t.Fatalf("file iteration case failed: %v", err)
	}
	if !strings.Contains(out, "line one") || !strings.Contains(out, "3") {
		t.Errorf("file iteration case output wrong:\n%s", out)
	}

	// 3. unsupported operand for <: 'tuple' and 'tuple' — sorted/sort on tuples
	out, err = tool.Execute(`{"code": "pairs = [(2, 'b'), (1, 'z'), (1, 'a'), (0, 5)]\npairs.sort()\nprint(pairs)\nprint((1, 2) < (1, 3))\nprint((3,) > (2, 9))"}`)
	if err != nil {
		t.Fatalf("tuple ordering case failed: %v", err)
	}
	if !strings.Contains(out, "[(0, 5), (1, 'a'), (1, 'z'), (2, 'b')]") {
		t.Errorf("tuple sort output wrong:\n%s", out)
	}
	if !strings.Contains(out, "True") {
		t.Errorf("tuple comparison output wrong:\n%s", out)
	}

	// 4. open() encoding kwarg — open(path, encoding='utf-8')
	out, err = tool.Execute(`{"code": "with open('data.bin', encoding='utf-8') as f:\n    content = f.read()\nprint('line one' in content)"}`)
	if err != nil {
		t.Fatalf("encoding kwarg case failed: %v", err)
	}
	if !strings.Contains(out, "True") {
		t.Errorf("encoding case output wrong:\n%s", out)
	}
}

// TestPythonTool_FStringConversion verifies the f-string !r/!s conversion
// shim rewrites them to repr()/str() calls the embedded parser accepts.
func TestPythonTool_FStringConversion(t *testing.T) {
	tool := &PythonTool{}
	out, err := tool.Execute(`{"code": "x = {'a': 1}\nprint(f\"{x!r}\")\nprint(f\"{42!s}\")\nprint(f\"plain {x['a']}\")"}`)
	if err != nil {
		t.Fatalf("conversion case failed: %v", err)
	}
	if !strings.Contains(out, "{'a': 1}") || !strings.Contains(out, "42") || !strings.Contains(out, "plain 1") {
		t.Errorf("unexpected output:\n%s", out)
	}
}
