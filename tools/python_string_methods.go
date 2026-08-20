// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"sync"
	"unicode/utf8"

	"github.com/pijalu/gpython/py"
)

// pyStringMethodsOnce guards the one-time registration of methods the embedded
// gpython py.StringType omits. gpython's own init registers split/strip/join/
// upper/lower/startswith/... but no splitlines, so idiomatic LLM Python such
// as `f.read().splitlines()` raised AttributeError. Registration is idempotent
// and patches the single global py.StringType, so it is available to every
// interpreter context.
var pyStringMethodsOnce sync.Once

// registerPyStringMethods patches the global py.StringType with the str
// methods gpython omits. It is safe to call from any interpreter setup path.
func registerPyStringMethods() {
	pyStringMethodsOnce.Do(func() {
		py.StringType.Dict["splitlines"] = py.MustNewMethod("splitlines",
			func(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
				return stringSplitlines(self, args, kwargs)
			}, 0, stringSplitlinesDoc)
	})
}

// stringSplitlines implements str.splitlines([keepends]) with full CPython
// semantics: the string is split at the CPython line-boundary set (\n, \r,
// \r\n, \v, \f, \x1c, \x1d, \x1e, \x85, \u2028, \u2029) with \r\n treated as a
// single boundary; terminators are stripped unless keepends is true; a
// trailing boundary does not produce a trailing empty element; the empty
// string yields an empty list.
func stringSplitlines(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	s, ok := self.(py.String)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError,
			"splitlines() requires a str object, not %s", self.Type().Name)
	}
	var keependsArg py.Object
	if err := py.UnpackTuple(args, kwargs, "splitlines", 0, 1, &keependsArg); err != nil {
		return nil, err
	}
	keepends, err := splitlinesKeepends(keependsArg)
	if err != nil {
		return nil, err
	}
	return py.NewListFromStrings(pySplitLines(string(s), keepends)), nil
}

// splitlinesKeepends validates the optional keepends argument. CPython
// accepts a truth value for keepends (bool or int); other types raise a
// TypeError.
func splitlinesKeepends(arg py.Object) (bool, error) {
	switch v := arg.(type) {
	case nil:
		return false, nil
	case py.Bool:
		return bool(v), nil
	case py.Int:
		return v != 0, nil
	}
	return false, py.ExceptionNewf(py.TypeError,
		"'%s' object cannot be interpreted as an integer", arg.Type().Name)
}

// isLineBoundary reports whether r is one of the CPython str.splitlines line
// boundaries: \n \r \v \f \x1c \x1d \x1e \x85 \u2028 \u2029.
// (\r\n is handled by the caller as a single two-byte boundary.)
func isLineBoundary(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f',
		'\x1c', '\x1d', '\x1e',
		'\x85', '\u2028', '\u2029':
		return true
	}
	return false
}

// pySplitLines splits s at CPython line boundaries, keeping or stripping the
// terminators. A trailing boundary does not yield a trailing empty element,
// so an empty input yields no lines at all (matching CPython). Named with a
// py prefix to avoid colliding with the tools package's line splitter.
func pySplitLines(s string, keepends bool) []string {
	var lines []string
	start := 0 // byte offset of the current line start
	i := 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !isLineBoundary(r) {
			i += size
			continue
		}
		end := i + size
		if r == '\r' && end < len(s) && s[end] == '\n' {
			end++ // \r\n is a single line boundary
		}
		if keepends {
			lines = append(lines, s[start:end])
		} else {
			lines = append(lines, s[start:i])
		}
		i, start = end, end
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

const stringSplitlinesDoc = `splitlines(keepends=False) -> list of str

Return a list of the lines in the string, breaking at line boundaries.
Line breaks are not included in the resulting list unless keepends is
given and true. Boundaries: \n, \r, \r\n, \v, \f, \x1c, \x1d, \x1e,
\x85, \u2028, \u2029.`

func init() {
	registerPyStringMethods()
}
