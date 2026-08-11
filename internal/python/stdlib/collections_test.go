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

// TestCollectionsCounterNonStrKeys pins the reported bug: Counter must
// accept non-string keys by normalizing them through str(key) instead of
// raising "Counter() argument must be str, not int".
func TestCollectionsCounterNonStrKeys(t *testing.T) {
	cases := []struct {
		name string
		code string
		want []string
	}{
		{
			name: "reported transcript: int key tally",
			code: `
import collections
c = collections.Counter()
c[200] = c[200] + 1
c[200] = c[200] + 1
print(c[200])
`,
			want: []string{"2"},
		},
		{
			name: "int key via constructor iterable",
			code: `
import collections
c = collections.Counter([200, 200, 404])
print(c[200])
print(c[404])
print(c[500])
`,
			want: []string{"2", "1", "0"},
		},
		{
			name: "int key and str key share the same bucket",
			code: `
import collections
c = collections.Counter()
c[200] = 5
print(c["200"])
print(c[200])
`,
			want: []string{"5", "5"},
		},
		{
			name: "bool key normalizes via str",
			code: `
import collections
c = collections.Counter()
c[True] = c[True] + 1
print(c["True"])
`,
			want: []string{"1"},
		},
		{
			name: "contains with int key",
			code: `
import collections
c = collections.Counter([200])
print(200 in c)
print(404 in c)
`,
			want: []string{"True", "False"},
		},
		{
			name: "update with int iterable",
			code: `
import collections
c = collections.Counter()
c.update([200, 200])
print(c[200])
`,
			want: []string{"2"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := pyCode(t, tc.code)
			if err != nil {
				t.Fatalf("error: %v\noutput: %s", err, out)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("expected %q in output, got: %s", want, out)
				}
			}
		})
	}
}

// TestCollectionsCounterGet pins the new .get(key, default=None) method,
// including the reported "c.get('a', 0)" idiom.
func TestCollectionsCounterGet(t *testing.T) {
	cases := []struct {
		name string
		code string
		want []string
	}{
		{
			name: "reported transcript: get with default",
			code: `
import collections
c = collections.Counter()
c.update(['a', 'b', 'a'])
print(c.get('a', 0))
print(c.get('zzz', 0))
`,
			want: []string{"2", "0"},
		},
		{
			name: "get missing key returns None default",
			code: `
import collections
c = collections.Counter(['a'])
print(c.get('nope'))
`,
			want: []string{"None"},
		},
		{
			name: "get with int key normalizes",
			code: `
import collections
c = collections.Counter()
c[200] = 3
print(c.get(200, 0))
print(c.get(404, -1))
`,
			want: []string{"3", "-1"},
		},
		{
			name: "get returns stored zero count",
			code: `
import collections
c = collections.Counter()
c['a'] = 0
print(c.get('a', 99))
`,
			want: []string{"0"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := pyCode(t, tc.code)
			if err != nil {
				t.Fatalf("error: %v\noutput: %s", err, out)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("expected %q in output, got: %s", want, out)
				}
			}
		})
	}
}

// TestCollectionsCounterGetArgErrors pins CPython arg validation for .get.
func TestCollectionsCounterGetArgErrors(t *testing.T) {
	cases := []struct {
		name string
		code string
	}{
		{name: "zero args", code: "import collections\ncollections.Counter().get()"},
		{name: "three args", code: "import collections\ncollections.Counter().get('a', 0, 1)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := pyCode(t, tc.code)
			if err == nil {
				t.Fatalf("expected TypeError, got success output: %s", out)
			}
			if !strings.Contains(err.Error(), "TypeError") {
				t.Errorf("expected TypeError, got: %v", err)
			}
		})
	}
}
