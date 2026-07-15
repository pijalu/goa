// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib_test

import (
	"strings"
	"testing"
)

func TestCollectionsCounter(t *testing.T) {
	code := `
import collections
c = collections.Counter(["a", "b", "a", "a", "b"])
print(c["a"])
print(c["b"])
print(c["c"])
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"3", "2", "0"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

func TestCollectionsCounterUpdate(t *testing.T) {
	code := `
import collections
c = collections.Counter(["a", "a"])
c.update(["a", "b"])
print(c["a"])
print(c["b"])
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"3", "1"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

func TestCollectionsCounterSubtract(t *testing.T) {
	code := `
import collections
c = collections.Counter(["a", "a", "a"])
c.subtract(["a", "b"])
print(c["a"])
print(c["b"])
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"2", "-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

func TestCollectionsCounterMostCommon(t *testing.T) {
	code := `
import collections
c = collections.Counter(["a", "a", "b", "b", "b"])
mc = c.most_common(1)
print(mc[0][0])
print(mc[0][1])
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"b", "3"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

func TestCollectionsCounterElements(t *testing.T) {
	code := `
import collections
c = collections.Counter(["a", "a", "b"])
print(len(list(c.elements())))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "3") {
		t.Errorf("expected 3 in output, got: %s", out)
	}
}

func TestCollectionsCounterSetItem(t *testing.T) {
	code := `
import collections
c = collections.Counter()
c["a"] = 5
print(c["a"])
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "5") {
		t.Errorf("expected 5 in output, got: %s", out)
	}
}

func TestCollectionsCounterContains(t *testing.T) {
	code := `
import collections
c = collections.Counter(["a"])
print("a" in c)
print("b" in c)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "True") || !strings.Contains(out, "False") {
		t.Errorf("expected True and False in output, got: %s", out)
	}
}

func TestCollectionsCounterFromDict(t *testing.T) {
	code := `
import collections
c = collections.Counter({"a": 2, "b": 3})
print(c["a"])
print(c["b"])
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"2", "3"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}
