// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib_test

import (
	"strings"
	"testing"
)

func TestItertoolsCount(t *testing.T) {
	code := `
import itertools
c = itertools.count(10, 2)
print(next(c))
print(next(c))
print(next(c))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"10", "12", "14"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

func TestItertoolsCycle(t *testing.T) {
	code := `
import itertools
c = itertools.cycle(["a", "b"])
print(next(c))
print(next(c))
print(next(c))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"a", "b", "a"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

func TestItertoolsRepeat(t *testing.T) {
	code := `
import itertools
r = itertools.repeat("x", 3)
print(next(r))
print(next(r))
print(next(r))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for i := 0; i < 3; i++ {
		if !strings.Contains(out, "x") {
			t.Errorf("expected x in output, got: %s", out)
		}
	}
}

func TestItertoolsChain(t *testing.T) {
	code := `
import itertools
c = itertools.chain([1, 2], [3, 4])
print(list(c))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "[1, 2, 3, 4]") {
		t.Errorf("expected [1, 2, 3, 4] in output, got: %s", out)
	}
}

func TestItertoolsISlice(t *testing.T) {
	code := `
import itertools
s = list(itertools.islice([0, 1, 2, 3, 4, 5], 1, 5, 2))
print(s)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "[1, 3]") {
		t.Errorf("expected [1, 3] in output, got: %s", out)
	}
}

func TestItertoolsCombinations(t *testing.T) {
	code := `
import itertools
c = list(itertools.combinations("abc", 2))
print(c)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "[['a', 'b'], ['a', 'c'], ['b', 'c']]") {
		t.Errorf("expected combinations in output, got: %s", out)
	}
}

func TestItertoolsPermutations(t *testing.T) {
	code := `
import itertools
p = list(itertools.permutations("ab", 2))
print(p)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "[['a', 'b'], ['b', 'a']]") {
		t.Errorf("expected permutations in output, got: %s", out)
	}
}

func TestItertoolsProduct(t *testing.T) {
	code := `
import itertools
p = list(itertools.product([1, 2], [3, 4]))
print(p)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "[[1, 3], [1, 4], [2, 3], [2, 4]]") {
		t.Errorf("expected product in output, got: %s", out)
	}
}
