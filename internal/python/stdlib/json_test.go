// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib_test

import (
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
