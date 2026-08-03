// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runPyInProject runs code through a PythonTool rooted at dir (jail on by
// default), returning stdout and any error.
func runPyInProject(t *testing.T, dir string, jail bool, code string) (string, error) {
	t.Helper()
	tool := &PythonTool{ProjectDir: dir, Jail: jail}
	return tool.Execute(`{"code": ` + jsonStr(code) + `}`)
}

// jsonStr produces a JSON string literal for code.
func jsonStr(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// pyQuote renders a Go string as a Python double-quoted literal.
func pyQuote(s string) string {
	return "\"" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + "\""
}

func TestPyFileScope_Resolve(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj")
	_ = os.MkdirAll(root, 0o755)
	scope := newPyFileScope(root, true)

	cases := []struct {
		name    string
		in      string
		wantSub string // result must equal root/wantSub
		wantErr string // empty => no error
	}{
		{"relative", "a.txt", "a.txt", ""},
		{"nested relative", "sub/a.txt", filepath.Join("sub", "a.txt"), ""},
		{"dot", ".", "", ""},
		{"absolute inside", filepath.Join(root, "x"), "x", ""},
		{"absolute outside", filepath.Join(t.TempDir(), "out"), "", "escape"},
		{"traversal", "../../etc/passwd", "", "escape"},
		{"dotdot back inside", filepath.Join(root, "..", "proj", "y"), "y", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertPyResolve(t, scope, tc.in, tc.wantSub, tc.wantErr)
		})
	}
}

// assertPyResolve checks a single resolve case against the expected result.
func assertPyResolve(t *testing.T, scope pyFileScope, in, wantSub, wantErr string) {
	t.Helper()
	got, err := scope.resolve(in)
	if wantErr != "" {
		if err == nil {
			t.Fatalf("resolve(%q): want error containing %q, got %q", in, wantErr, got)
		}
		if !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("resolve(%q): error %q does not contain %q", in, err.Error(), wantErr)
		}
		return
	}
	if err != nil {
		t.Fatalf("resolve(%q): unexpected error: %v", in, err)
	}
	want := filepath.Join(scope.root, wantSub)
	if got != want {
		t.Errorf("resolve(%q) = %q, want %q", in, got, want)
	}
}

func TestPyFileScope_Unconfined(t *testing.T) {
	scope := newPyFileScope("", false)
	got, err := scope.resolve("some/relative/path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("unconfined resolve should be absolute, got %q", got)
	}
}

func TestPathUnderAbsRoot(t *testing.T) {
	root := filepath.Clean("/tmp/proj")
	tests := []struct {
		abs, root string
		want      bool
	}{
		{"/tmp/proj", root, true},
		{"/tmp/proj/x", root, true},
		{"/tmp/proj/sub/deep", root, true},
		{"/tmp/project", root, false},
		{"/tmp/projx", root, false},
		{"/etc/passwd", root, false},
	}
	for _, tc := range tests {
		if got := pathUnderAbsRoot(tc.abs, tc.root); got != tc.want {
			t.Errorf("pathUnderAbsRoot(%q,%q) = %v, want %v", tc.abs, tc.root, got, tc.want)
		}
	}
}

func TestOS_ListdirGetcwdAndConstants(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o644)
	out, err := runPyInProject(t, dir, true, `
import os
print("cwd is root:", os.getcwd() == `+pyQuote(dir)+`)
print("name:", os.name)
print("sep:", os.sep)
print("sorted:", sorted(os.listdir(".")))
`)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	for _, w := range []string{"cwd is root: True", "name: posix", "sep: /", "sorted: ['a.go']"} {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\nfull:\n%s", w, out)
		}
	}
}

func TestOS_PathBasics(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello"), 0o644)
	out, err := runPyInProject(t, dir, true, `
import os
p = os.path.join("a", "b.go")
print("join:", p)
print("basename:", os.path.basename(p))
print("dirname:", os.path.dirname(p))
print("ext:", os.path.splitext("x.tar.gz"))
print("isabs:", os.path.isabs("/x"))
print("exists:", os.path.exists("f.txt"), os.path.exists("nope"))
print("isfile:", os.path.isfile("f.txt"))
print("isdir:", os.path.isdir("nope"))
print("getsize:", os.path.getsize("f.txt"))
print("abspath:", os.path.abspath("f.txt") == os.path.join(os.getcwd(), "f.txt"))
`)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	for _, w := range []string{
		"join: a/b.go", "basename: b.go", "dirname: a",
		"ext: ('x.tar', '.gz')", "isabs: True",
		"exists: True False", "isfile: True", "isdir: False",
		"getsize: 5", "abspath: True",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\nfull:\n%s", w, out)
		}
	}
}

func TestOS_StatAttributes(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "f.txt"), []byte("12345678"), 0o644)
	out, err := runPyInProject(t, dir, true, `
import os
s = os.stat("f.txt")
print("size:", s.st_size)
print("size2:", os.stat("f.txt").st_size + 2)
print("mode_is_int:", isinstance(s.st_mode, int))
print("mtime_is_float:", isinstance(s.st_mtime, float))
`)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	for _, w := range []string{"size: 8", "size2: 10", "mode_is_int: True", "mtime_is_float: True"} {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\nfull:\n%s", w, out)
		}
	}
}

func TestOS_MkdirMakedirsRemove(t *testing.T) {
	dir := t.TempDir()
	out, err := runPyInProject(t, dir, true, `
import os
os.mkdir("d1")
os.makedirs("a/b/c")
print("d1:", os.path.isdir("d1"))
print("abc:", os.path.isdir("a/b/c"))
# idempotent makedirs
os.makedirs("a/b/c", exist_ok=True)
print("ok")
with open(os.path.join("d1", "f.txt"), "w") as fh:
    fh.write("hi")
os.remove(os.path.join("d1", "f.txt"))
os.rmdir("d1")
print("removed:", os.path.exists("d1"))
`)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	for _, w := range []string{"d1: True", "abc: True", "removed: False"} {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\nfull:\n%s", w, out)
		}
	}
}

// TestOS_ProjectStatistics is the motivating regression test: the export
// bundle showed the model failing to summarize the project because os.walk /
// os.stat / os.path.join were unavailable in the stock gpython stdlib.
func TestOS_ProjectStatistics(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "a_test.go"), []byte("package main\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "cmd"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "cmd", "main.go"), []byte("package main\nfunc main(){}\n"), 0o644)

	out, err := runPyInProject(t, dir, true, `
import os
total = 0
go_files = 0
test_files = 0
for dirpath, dirnames, filenames in os.walk("."):
    for name in filenames:
        path = os.path.join(dirpath, name)
        if name.endswith(".go"):
            if name.endswith("_test.go"):
                test_files += 1
            else:
                go_files += 1
                total += os.stat(path).st_size
print("go:", go_files, "test:", test_files, "bytes:", total)
`)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "go: 2 test: 1") {
		t.Errorf("output = %q", out)
	}
}

func TestOS_JailEscapeBlocked(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	_ = os.WriteFile(outsideFile, []byte("nope"), 0o644)

	cases := map[string]string{
		"walk":    `import os; os.walk(` + pyQuote(outside) + `)`,
		"stat":    `import os; os.stat(` + pyQuote(outsideFile) + `)`,
		"exists":  `import os; os.path.exists(` + pyQuote(outsideFile) + `)`,
		"abspath": `import os; os.path.abspath(` + pyQuote(outsideFile) + `)`,
		"remove":  `import os; os.remove(` + pyQuote(outsideFile) + `)`,
	}
	for name, code := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := runPyInProject(t, dir, true, code)
			if err == nil {
				t.Fatalf("expected PermissionError for %s", name)
			}
			if !strings.Contains(err.Error(), "PermissionError") {
				t.Errorf("%s: error = %v", name, err)
			}
		})
	}
}

func TestOS_JailTraversalBlocked(t *testing.T) {
	dir := t.TempDir()
	_, err := runPyInProject(t, dir, true, `import os; os.stat("../../../../etc/passwd")`)
	if err == nil || !strings.Contains(err.Error(), "PermissionError") {
		t.Fatalf("expected PermissionError, got: %v", err)
	}
}

func TestOS_UnconfinedAllowsOutside(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "ok.txt")
	_ = os.WriteFile(target, []byte("data"), 0o644)
	out, err := runPyInProject(t, dir, false, `
import os
print(os.stat(`+pyQuote(target)+`).st_size)
`)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "4") {
		t.Errorf("output = %q", out)
	}
}

// TestOS_UnsafePrimitivesRemoved ensures os.system and os._exit are not exposed.
func TestOS_UnsafePrimitivesRemoved(t *testing.T) {
	dir := t.TempDir()
	for _, attr := range []string{"system", "_exit"} {
		_, err := runPyInProject(t, dir, true, `import os; getattr(os, `+pyQuote(attr)+`)`)
		if err == nil || !strings.Contains(err.Error(), "AttributeError") {
			t.Errorf("expected os.%s to be absent (AttributeError), got: %v", attr, err)
		}
	}
}

func TestOS_EnvironAndGetenv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOA_PY_TEST", "abc123")
	out, err := runPyInProject(t, dir, true, `
import os
print(os.environ["GOA_PY_TEST"])
print(os.getenv("GOA_PY_TEST"))
print(os.getenv("MISSING", "def"))
print(isinstance(os.getpid(), int))
`)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	for _, w := range []string{"abc123", "def", "True"} {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\nfull:\n%s", w, out)
		}
	}
}

func TestOS_OpenRoundtrip(t *testing.T) {
	dir := t.TempDir()
	out, err := runPyInProject(t, dir, true, `
f = open("note.txt", "w")
f.write("hello")
f.close()
with open("note.txt") as g:
    print(g.read())
`)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("output = %q", out)
	}
}

// TestOS_OpenErrorsHandling replays the e2e failure where the standard
// CPython idiom open(path, "r", errors="replace") — used habitually when
// reading logs that may contain non-UTF-8 bytes — died with
// `TypeError: "open() got an unexpected keyword argument 'errors'"` because
// the jail-confined open() shadow only accepted file/mode/encoding.
//
// Assertions run inside Python and print booleans: the tool output path
// (ansi.Sanitize) itself replaces invalid UTF-8 with U+FFFD, so raw output
// cannot distinguish byte passthrough from errors="replace".
// writePyFixture writes content to a file in dir for python snippets.
func writePyFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOS_OpenErrorsHandling(t *testing.T) {
	dir := t.TempDir()
	// 0xff 0xfe is an invalid UTF-8 run between two valid segments.
	writePyFixture(t, dir, "bad.log", "valid-\xff\xfef\xc3\xa9-tail")
	// ANSI-laced log lines for the original-snippet replay.
	writePyFixture(t, dir, "run1.log", "\x1b[32mok line\x1b[0m\nbad \xff bytes line\nok line\n")

	cases := []struct {
		name    string
		code    string
		want    string // substring expected in stdout
		wantErr bool
	}{
		{"replace", `
with open("bad.log", "r", errors="replace") as f:
    s = f.read()
print("valid-" in s and "-tail" in s)
print("\ufffd" in s)
`, "True\nTrue", false},
		{"ignore", `
with open("bad.log", "r", errors="ignore") as f:
    s = f.read()
print("valid-f" in s)
print("\ufffd" in s)
`, "True\nFalse", false},
		// Documented divergence: gpython strings are byte-oriented, so the
		// default (and explicit strict) passes bytes through unchanged
		// instead of raising UnicodeDecodeError — no U+FFFD appears.
		{"strict-default", `s = open("bad.log").read(); print("\ufffd" in s)`, "False", false},
		{"strict-explicit", `s = open("bad.log", errors="strict").read(); print("\ufffd" in s)`, "False", false},
		{"unknown-handler", `open("bad.log", errors="bogus")`, "", true},
		// The exact script shape from the e2e failure log: ANSI-laced log
		// lines, errors="replace", re.sub cleanup, dedup loop.
		{"original-snippet", `
import re

raw = open("run1.log", "r", errors="replace").read()
clean = re.sub(r'\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07]*\x07|\x1b\(\w|\r', '', raw)
lines = [re.sub(r'\s+', ' ', l).strip() for l in clean.split('\n')]
lines = [l for l in lines if l]

seen_order = []
for l in lines:
    if not seen_order or seen_order[-1] != l:
        seen_order.append(l)
print(seen_order)
`, "ok line", false},
		{"binary-mode-unfiltered", `
data = open("bad.log", "rb").read()
print(isinstance(data, bytes), len(data))
`, "True 16", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runPyInProject(t, dir, true, tc.code)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("execute: %v\n%s", err, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output missing %q\nfull:\n%s", tc.want, out)
			}
		})
	}
}

func TestOS_OpenJailEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "secret.txt")
	_ = os.WriteFile(target, []byte("nope"), 0o644)
	for _, mode := range []string{"r", "w"} {
		_, err := runPyInProject(t, dir, true, `open(`+pyQuote(target)+`, `+pyQuote(mode)+`)`)
		if err == nil || !strings.Contains(err.Error(), "PermissionError") {
			t.Errorf("open(%q) mode %q: expected PermissionError, got: %v", target, mode, err)
		}
	}
	if _, err := os.Stat(target); err != nil && os.IsNotExist(err) {
		// fine
	}
}

// TestOS_StockOSIsShadowed confirms `import os` yields the curated module, not
// the sparse stock gpython os (which lacks walk/stat and exposes system).
func TestOS_StockOSIsShadowed(t *testing.T) {
	dir := t.TempDir()
	out, err := runPyInProject(t, dir, true, `
import os
print("walk=", hasattr(os, "walk"))
print("stat=", hasattr(os, "stat"))
print("path=", hasattr(os, "path"))
print("system=", hasattr(os, "system"))
`)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	for _, w := range []string{"walk= True", "stat= True", "path= True", "system= False"} {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\nfull:\n%s", w, out)
		}
	}
}
