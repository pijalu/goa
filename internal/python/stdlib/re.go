// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib

import (
	"regexp"
	"strings"

	"github.com/pijalu/gpython/py"

	"github.com/pijalu/goa/internal/python/compat"
)

// Flag constants for the re module.
const (
	reFlagIgnoreCase = 1 << iota
	reFlagMultiline
	reFlagDotAll
	reFlagVerbose
)

// Pattern is a compiled regular expression.
type Pattern struct {
	re      *regexp.Regexp
	pattern string
	flags   int
}

var patternType = py.NewType("Pattern", `Pattern(pattern, flags=0)

Compiled regular expression object.`)

// Type returns the Pattern type.
func (p *Pattern) Type() *py.Type { return patternType }

// M__getattr__ exposes pattern and flags as read-only attributes.
func (p *Pattern) M__getattr__(name string) (py.Object, error) {
	switch name {
	case "pattern":
		return py.String(p.pattern), nil
	case "flags":
		return py.Int(p.flags), nil
	}
	return nil, py.ExceptionNewf(py.AttributeError, "'Pattern' object has no attribute '%s'", name)
}

// Match represents a match result.
type Match struct {
	pattern *Pattern
	text    string
	indices []int // from FindStringSubmatchIndex
}

var matchType = py.NewType("Match", `Match

The result of a regex match.`)

// Type returns the Match type.
func (m *Match) Type() *py.Type { return matchType }

// M__getattr__ exposes string and re as read-only attributes.
func (m *Match) M__getattr__(name string) (py.Object, error) {
	switch name {
	case "string":
		return py.String(m.text), nil
	case "re":
		return m.pattern, nil
	}
	return nil, py.ExceptionNewf(py.AttributeError, "'Match' object has no attribute '%s'", name)
}

func init() {
	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "re",
			Doc: `re — regular expression operations.

This module provides regular expression matching operations similar to
those found in Python's built-in re module. It is backed by Go's RE2
engine, which means it does not support lookahead, lookbehind, or
backreferences.

Supported flags: I/IGNORECASE, M/MULTILINE, S/DOTALL, X/VERBOSE.
VERBOSE is implemented by stripping unescaped whitespace and comments
outside character classes before handing the pattern to RE2.

CPython parity note: finditer returns a LIST of Match objects rather
than a lazy iterator (Py3.4-subset pragmatism); 'for m in
re.finditer(...)' and 'list(...)' usage works identically.`,
		},
		Methods: []*py.Method{
			py.MustNewMethod("compile", reCompile, 0, `compile(pattern, flags=0) -> Pattern

Compile a regular expression pattern, returning a Pattern object.`),
			py.MustNewMethod("search", reSearch, 0, `search(pattern, string, flags=0) -> Match | None

Scan through string looking for the first location where the pattern
produces a match.`),
			py.MustNewMethod("match", reMatch, 0, `match(pattern, string, flags=0) -> Match | None

If zero or more characters at the beginning of string match the
regular expression pattern, return a corresponding Match object.`),
			py.MustNewMethod("findall", reFindall, 0, `findall(pattern, string, flags=0) -> list[str]

 Return all non-overlapping matches of pattern in string as a list
 of strings.`),
			py.MustNewMethod("finditer", reFinditer, 0, `finditer(pattern, string, flags=0) -> list[Match]

 Return all non-overlapping matches of pattern in string as a list
 of Match objects (CPython returns a lazy iterator; see module doc).`),
			py.MustNewMethod("sub", reSub, 0, `sub(pattern, repl, string, flags=0) -> str

	Return the string obtained by replacing the leftmost non-overlapping
	occurrences of pattern in string by the replacement repl. If repl is
	a string, \1/\g<1>/\g<name> group references and \\ escapes are
	expanded (CPython template semantics). If repl is a callable, it is
	invoked per match with the Match object and its return value is used
	as the replacement.`),
			py.MustNewMethod("split", reSplit, 0, `split(pattern, string, flags=0) -> list[str]

Split string by the occurrences of pattern.`),
			py.MustNewMethod("escape", reEscape, 0, `escape(string) -> str

Escape all non-alphanumeric characters in pattern.`),
		},
		Globals: py.StringDict{
			"I":          py.Int(reFlagIgnoreCase),
			"IGNORECASE": py.Int(reFlagIgnoreCase),
			"M":          py.Int(reFlagMultiline),
			"MULTILINE":  py.Int(reFlagMultiline),
			"S":          py.Int(reFlagDotAll),
			"DOTALL":     py.Int(reFlagDotAll),
			"X":          py.Int(reFlagVerbose),
			"VERBOSE":    py.Int(reFlagVerbose),
		},
	})

	// Register Pattern methods
	patternType.Dict["search"] = py.MustNewMethod("search", patternSearch, 0, `search(string) -> Match | None

Scan through string looking for the first location where this pattern
produces a match.`)
	patternType.Dict["match"] = py.MustNewMethod("match", patternMatch, 0, `match(string) -> Match | None

If zero or more characters at the beginning of string match this
pattern, return a corresponding Match object.`)
	patternType.Dict["findall"] = py.MustNewMethod("findall", patternFindall, 0, `findall(string) -> list[str]

 Return all non-overlapping matches of this pattern in string as a
 list of strings.`)
	patternType.Dict["finditer"] = py.MustNewMethod("finditer", patternFinditer, 0, `finditer(string) -> list[Match]

 Return all non-overlapping matches of this pattern in string as a
 list of Match objects (CPython returns a lazy iterator; see module
 doc).`)
	patternType.Dict["sub"] = py.MustNewMethod("sub", patternSub, 0, `sub(repl, string) -> str

Return the string obtained by replacing the leftmost non-overlapping
occurrences of this pattern in string by the replacement repl. repl
may be a template string (\1, \g<1>, \g<name>) or a callable taking
the Match object.`)
	patternType.Dict["split"] = py.MustNewMethod("split", patternSplit, 0, `split(string) -> list[str]

Split string by the occurrences of this pattern.`)

	// Register Match methods
	matchType.Dict["group"] = py.MustNewMethod("group", matchGroup, 0, `group([group1, ...]) -> str | tuple

Returns one or more subgroups of the match. If a single argument is
given, the result is a string; if multiple arguments, the result is
a tuple of strings.`)
	matchType.Dict["start"] = py.MustNewMethod("start", matchStart, 0, `start([group=0]) -> int

Return the start position of the subgroup identified by group.`)
	matchType.Dict["end"] = py.MustNewMethod("end", matchEnd, 0, `end([group=0]) -> int

Return the end position of the subgroup identified by group.`)
	matchType.Dict["span"] = py.MustNewMethod("span", matchSpan, 0, `span([group=0]) -> tuple[int, int]

Return a 2-tuple (start, end) for the subgroup identified by group.`)
}

// --- Module-level functions ---

// reCompile implements re.compile(pattern, flags=0).
func reCompile(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	var pattern py.Object
	var flags py.Object = py.Int(0)
	if err := parsePatternAndFlags(args, kwargs, "compile", &pattern, &flags); err != nil {
		return nil, err
	}
	patStr, err := compileFromObjects(pattern, flags, "compile")
	if err != nil {
		return nil, err
	}
	return patStr, nil
}

// reSearch implements re.search(pattern, string, flags=0).
func reSearch(self py.Object, args py.Tuple) (py.Object, error) {
	var flags int64
	pat, str, err := parseTwoOrThreeArgs(args, "search", &flags)
	if err != nil {
		return nil, err
	}
	return doSearchMatch(pat, str, "search", true, int(flags))
}

// reMatch implements re.match(pattern, string, flags=0).
func reMatch(self py.Object, args py.Tuple) (py.Object, error) {
	var flags int64
	pat, str, err := parseTwoOrThreeArgs(args, "match", &flags)
	if err != nil {
		return nil, err
	}
	return doSearchMatch(pat, str, "match", false, int(flags))
}

// reFindall implements re.findall(pattern, string, flags=0).
func reFindall(self py.Object, args py.Tuple) (py.Object, error) {
	var flags int64
	pat, str, err := parseTwoOrThreeArgs(args, "findall", &flags)
	if err != nil {
		return nil, err
	}
	pp, txt, err := compileAndGetText(pat, str, "findall", int(flags))
	if err != nil {
		return nil, err
	}
	matches := pp.re.FindAllString(txt, -1)
	items := make([]py.Object, len(matches))
	for i, m := range matches {
		items[i] = py.String(m)
	}
	return py.NewListFromItems(items), nil
}

// reFinditer implements re.finditer(pattern, string, flags=0). CPython
// returns a lazy iterator; the Py3.4-subset pragmatism here returns a LIST
// of Match objects (see the module doc) — 'for m in re.finditer(...)' and
// 'list(...)' usage is identical.
func reFinditer(self py.Object, args py.Tuple) (py.Object, error) {
	var flags int64
	pat, str, err := parseTwoOrThreeArgs(args, "finditer", &flags)
	if err != nil {
		return nil, err
	}
	pp, txt, err := compileAndGetText(pat, str, "finditer", int(flags))
	if err != nil {
		return nil, err
	}
	return finditerMatches(pp, txt), nil
}

// finditerMatches returns all non-overlapping matches of pp in txt as a
// py.List of *Match objects ([] when nothing matches).
func finditerMatches(pp *Pattern, txt string) py.Object {
	all := pp.re.FindAllStringSubmatchIndex(txt, -1)
	items := make([]py.Object, len(all))
	for i, indices := range all {
		items[i] = &Match{pattern: pp, text: txt, indices: indices}
	}
	return py.NewListFromItems(items)
}

// reSub implements re.sub(pattern, repl, string, flags=0). repl may be a
// template string (with \1-style and \g<name> group references, CPython
// semantics) or a callable taking a Match and returning the replacement.
func reSub(self py.Object, args py.Tuple) (py.Object, error) {
	var pattern, repl, str, flagsObj py.Object
	if err := py.UnpackTuple(args, nil, "sub", 3, 4, &pattern, &repl, &str, &flagsObj); err != nil {
		return nil, err
	}
	flags := 0
	if len(args) >= 4 {
		fl, err := compat.AsInt(flagsObj, "sub")
		if err != nil {
			return nil, err
		}
		flags = int(fl)
	}
	pp, err := toPattern("sub", pattern, flags)
	if err != nil {
		return nil, err
	}
	txt, err := compat.AsString(str, "sub")
	if err != nil {
		return nil, err
	}
	return substituteAll(pp, txt, repl, "sub")
}

// reSplit implements re.split(pattern, string, flags=0).
func reSplit(self py.Object, args py.Tuple) (py.Object, error) {
	var flags int64
	pat, str, err := parseTwoOrThreeArgs(args, "split", &flags)
	if err != nil {
		return nil, err
	}
	pp, txt, err := compileAndGetText(pat, str, "split", int(flags))
	if err != nil {
		return nil, err
	}
	parts := pp.re.Split(txt, -1)
	items := make([]py.Object, len(parts))
	for i, p := range parts {
		items[i] = py.String(p)
	}
	return py.NewListFromItems(items), nil
}

// reEscape implements re.escape(string).
func reEscape(self py.Object, args py.Tuple) (py.Object, error) {
	var s py.Object
	if err := py.UnpackTuple(args, nil, "escape", 1, 1, &s); err != nil {
		return nil, err
	}
	str, err := compat.AsString(s, "escape")
	if err != nil {
		return nil, err
	}
	return py.String(regexp.QuoteMeta(str)), nil
}

// --- Pattern methods ---

func patternSearch(self py.Object, args py.Tuple) (py.Object, error) {
	var str py.Object
	if err := py.UnpackTuple(args, nil, "search", 1, 1, &str); err != nil {
		return nil, err
	}
	return doSearchMatchOn(self, str, true)
}

func patternMatch(self py.Object, args py.Tuple) (py.Object, error) {
	var str py.Object
	if err := py.UnpackTuple(args, nil, "match", 1, 1, &str); err != nil {
		return nil, err
	}
	return doSearchMatchOn(self, str, false)
}

func patternFindall(self py.Object, args py.Tuple) (py.Object, error) {
	var str py.Object
	if err := py.UnpackTuple(args, nil, "findall", 1, 1, &str); err != nil {
		return nil, err
	}
	pp, ok := self.(*Pattern)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected Pattern, got %s", self.Type().Name)
	}
	txt, err := compat.AsString(str, "findall")
	if err != nil {
		return nil, err
	}
	matches := pp.re.FindAllString(txt, -1)
	items := make([]py.Object, len(matches))
	for i, m := range matches {
		items[i] = py.String(m)
	}
	return py.NewListFromItems(items), nil
}

// patternFinditer implements Pattern.finditer(string). Like the module-level
// finditer, returns a LIST of Match objects (see the module doc).
func patternFinditer(self py.Object, args py.Tuple) (py.Object, error) {
	var str py.Object
	if err := py.UnpackTuple(args, nil, "finditer", 1, 1, &str); err != nil {
		return nil, err
	}
	pp, ok := self.(*Pattern)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected Pattern, got %s", self.Type().Name)
	}
	txt, err := compat.AsString(str, "finditer")
	if err != nil {
		return nil, err
	}
	return finditerMatches(pp, txt), nil
}

// patternSub implements Pattern.sub(repl, string); like re.sub, repl may be
// a template string or a callable taking a Match.
func patternSub(self py.Object, args py.Tuple) (py.Object, error) {
	var repl, str py.Object
	if err := py.UnpackTuple(args, nil, "sub", 2, 2, &repl, &str); err != nil {
		return nil, err
	}
	pp, ok := self.(*Pattern)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected Pattern, got %s", self.Type().Name)
	}
	txt, err := compat.AsString(str, "sub")
	if err != nil {
		return nil, err
	}
	return substituteAll(pp, txt, repl, "sub")
}

// substituteAll replaces every non-overlapping match of pp in txt. A
// callable repl is invoked per match with the Match object and its return
// value (coerced to str) is spliced in — exceptions propagate unchanged. A
// str/bytes repl is expanded as a CPython replacement template (\1, \g<1>,
// \g<name>); a template without escapes takes the literal fast path.
func substituteAll(pp *Pattern, txt string, repl py.Object, fn string) (py.Object, error) {
	if _, callable := repl.(py.I__call__); callable {
		return substituteCallable(pp, txt, repl, fn)
	}
	tpl, err := compat.AsString(repl, fn)
	if err != nil {
		return nil, py.ExceptionNewf(py.TypeError,
			"%s() repl must be a string or callable, not %s", fn, repl.Type().Name)
	}
	if !strings.ContainsRune(tpl, '\\') {
		return py.String(pp.re.ReplaceAllLiteralString(txt, tpl)), nil
	}
	return substituteTemplate(pp, txt, tpl)
}

// substituteCallable calls repl(match) for each match of pp in txt and
// splices the stringified results in place of the matched text.
func substituteCallable(pp *Pattern, txt string, repl py.Object, fn string) (py.Object, error) {
	matches := pp.re.FindAllStringSubmatchIndex(txt, -1)
	if len(matches) == 0 {
		return py.String(txt), nil
	}
	var b strings.Builder
	b.Grow(len(txt))
	last := 0
	for _, indices := range matches {
		b.WriteString(txt[last:indices[0]])
		res, err := py.Call(repl, py.Tuple{&Match{pattern: pp, text: txt, indices: indices}}, nil)
		if err != nil {
			return nil, err
		}
		piece, err := compat.AsString(res, fn)
		if err != nil {
			return nil, err
		}
		b.WriteString(piece)
		last = indices[1]
	}
	b.WriteString(txt[last:])
	return py.String(b.String()), nil
}

// substituteTemplate expands a CPython-style replacement template for each
// match of pp in txt: \1..\99 reference groups by number, \g<N> and
// \g<name> reference groups by number or name, \\ yields a literal
// backslash, and \<ASCII-letter> otherwise raises CPython's 'bad escape'
// error. Out-of-range groups raise CPython's 'invalid group reference'.
func substituteTemplate(pp *Pattern, txt, tpl string) (py.Object, error) {
	matches := pp.re.FindAllStringSubmatchIndex(txt, -1)
	if len(matches) == 0 {
		return py.String(txt), nil
	}
	var b strings.Builder
	b.Grow(len(txt))
	last := 0
	for _, indices := range matches {
		expanded, err := expandTemplate(pp, txt, tpl, indices)
		if err != nil {
			return nil, err
		}
		b.WriteString(txt[last:indices[0]])
		b.WriteString(expanded)
		last = indices[1]
	}
	b.WriteString(txt[last:])
	return py.String(b.String()), nil
}

// expandTemplate renders a single match's replacement from tpl.
func expandTemplate(pp *Pattern, txt, tpl string, indices []int) (string, error) {
	m := &Match{pattern: pp, text: txt, indices: indices}
	var b strings.Builder
	for i := 0; i < len(tpl); i++ {
		consumed, err := expandTemplateByte(pp, m, tpl, i, &b)
		if err != nil {
			return "", err
		}
		i += consumed
	}
	return b.String(), nil
}
