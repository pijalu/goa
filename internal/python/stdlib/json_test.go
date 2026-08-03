// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJsonLoads(t *testing.T) {
	code := `
import json
obj = json.loads('{"a": 1, "b": [2, 3], "c": true, "d": null}')
print(obj["a"])
print(obj["b"][0])
print(obj["b"][1])
print(obj["c"])
print(obj["d"])
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"1", "2", "3", "True", "None"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

func TestJsonLoadsInvalid(t *testing.T) {
	code := `
import json
try:
    json.loads("not json")
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

func TestJsonDumps(t *testing.T) {
	code := `
import json
s = json.dumps({"a": 1, "b": [2, 3]})
print(s)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, `"a"`) || !strings.Contains(out, `"b"`) {
		t.Errorf("expected JSON object in output, got: %s", out)
	}
}

func TestJsonDumpsIndent(t *testing.T) {
	code := `
import json
s = json.dumps({"a": 1}, indent="  ")
print(s)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "\n") {
		t.Errorf("expected indented output with newline, got: %s", out)
	}
}

// TestJsonDumpsIntIndent covers CPython's int indent form (number of spaces):
// positive ints indent, zero/negative give newlines only, bool acts as int
// (CPython: bool is a subclass of int). Regression test for
// "TypeError: 'dumps() indent must be str or None, not int".
// repr() is printed so newlines arrive as literal \n text (escaping-safe).
func TestJsonDumpsIntIndent(t *testing.T) {
	code := `
import json
print(repr(json.dumps({"a": 1}, indent=2)))
print(repr(json.dumps({"a": 1}, indent=0)))
print(json.dumps({"a": 1}, indent=-1) == json.dumps({"a": 1}, indent=0))
print(repr(json.dumps({"a": 1}, indent=True)))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{
		`'{\n  "a": 1\n}'`, // indent=2: two spaces
		`'{\n"a": 1\n}'`,   // indent=0: newlines only
		"True",             // indent=-1 ≡ indent=0
		`'{\n "a": 1\n}'`,  // indent=True: one space
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

func TestJsonRoundTrip(t *testing.T) {
	code := `
import json
obj = {"x": 10, "y": [1, 2, 3], "z": False}
rt = json.loads(json.dumps(obj))
print(rt["x"])
print(rt["y"][2])
print(rt["z"])
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"10", "3", "False"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

func TestJsonDumpsUnsupportedType(t *testing.T) {
	code := `
import json
try:
    json.dumps(object())
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

func TestJsonLoadsNonString(t *testing.T) {
	code := `
import json
try:
    json.loads(123)
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

func TestJsonLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	if err := os.WriteFile(path, []byte(`{"a": 1, "b": [2, 3], "c": true, "d": null}`), 0o644); err != nil {
		t.Fatal(err)
	}
	code := fmt.Sprintf(`
import json
with open(%q) as f:
    data = json.load(f)
print(data["a"])
print(data["b"][1])
print(data["c"])
print(data["d"])
`, path)
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"1", "3", "True", "None"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

func TestJsonDump(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.json")
	code := fmt.Sprintf(`
import json
with open(%q, "w") as f:
    ret = json.dump({"x": 10, "y": [1, 2]}, f)
print(ret)
`, path)
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	// CPython's json.dump returns None.
	if !strings.Contains(out, "None") {
		t.Errorf("expected dump() to return None, got: %s", out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read dumped file: %v", err)
	}
	if !strings.Contains(string(data), `"x":10`) || !strings.Contains(string(data), `"y":[1,2]`) {
		t.Errorf("unexpected dumped content: %s", data)
	}
}

func TestJsonDumpLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rt.json")
	code := fmt.Sprintf(`
import json
obj = {"x": 10, "y": [1, 2, 3], "z": False}
with open(%q, "w") as f:
    json.dump(obj, f)
with open(%q) as f:
    rt = json.load(f)
print(rt["x"])
print(rt["y"][2])
print(rt["z"])
`, path, path)
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"10", "3", "False"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

func TestJsonDumpIndent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pretty.json")
	code := fmt.Sprintf(`
import json
with open(%q, "w") as f:
    json.dump({"a": 1}, f, indent="  ")
`, path)
	if _, err := pyCode(t, code); err != nil {
		t.Fatalf("error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\n") {
		t.Errorf("expected indented output with newline, got: %s", data)
	}
}

func TestJsonLoadNotAFile(t *testing.T) {
	code := `
import json
try:
    json.load(42)
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

func TestJsonDumpNotAFile(t *testing.T) {
	code := `
import json
try:
    json.dump({"a": 1}, 42)
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
