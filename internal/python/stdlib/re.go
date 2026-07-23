// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib

import (
	"regexp"

	"github.com/pijalu/gpython/py"

	"github.com/pijalu/goa/internal/python/compat"
)

// Flag constants for the re module. Only IGNORECASE is supported in the first pass.
const (
	reFlagIgnoreCase = 1 << iota
	_                // reserved for re.M (MULTILINE)
	_                // reserved for re.S (DOTALL)
	_                // reserved for re.X (VERBOSE)
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

Supported flags: I, IGNORECASE
Unsupported in the first pass: M (MULTILINE), S (DOTALL), X (VERBOSE).`,
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
			py.MustNewMethod("sub", reSub, 0, `sub(pattern, repl, string, flags=0) -> str

Return the string obtained by replacing the leftmost non-overlapping
occurrences of pattern in string by the replacement repl. The repl
is a literal string; backreferences ($1, \1) are not supported.`),
			py.MustNewMethod("split", reSplit, 0, `split(pattern, string, flags=0) -> list[str]

Split string by the occurrences of pattern.`),
			py.MustNewMethod("escape", reEscape, 0, `escape(string) -> str

Escape all non-alphanumeric characters in pattern.`),
		},
		Globals: py.StringDict{
			"I":          py.Int(reFlagIgnoreCase),
			"IGNORECASE": py.Int(reFlagIgnoreCase),
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
	patternType.Dict["sub"] = py.MustNewMethod("sub", patternSub, 0, `sub(repl, string) -> str

Return the string obtained by replacing the leftmost non-overlapping
occurrences of this pattern in string by the replacement repl.`)
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

// reSub implements re.sub(pattern, repl, string, flags=0).
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
	replStr, err := compat.AsString(repl, "sub")
	if err != nil {
		return nil, err
	}
	return py.String(pp.re.ReplaceAllLiteralString(txt, replStr)), nil
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
	replStr, err := compat.AsString(repl, "sub")
	if err != nil {
		return nil, err
	}
	return py.String(pp.re.ReplaceAllLiteralString(txt, replStr)), nil
}

func patternSplit(self py.Object, args py.Tuple) (py.Object, error) {
	var str py.Object
	if err := py.UnpackTuple(args, nil, "split", 1, 1, &str); err != nil {
		return nil, err
	}
	pp, ok := self.(*Pattern)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected Pattern, got %s", self.Type().Name)
	}
	txt, err := compat.AsString(str, "split")
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

// --- Match methods ---

func matchGroup(self py.Object, args py.Tuple) (py.Object, error) {
	m, ok := self.(*Match)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected Match, got %s", self.Type().Name)
	}
	if len(args) <= 1 {
		// For now, only support group() or group(n). Multi-group returns in a future pass.
		n := 0
		if len(args) == 1 {
			nArg, err := compat.AsInt(args[0], "group")
			if err != nil {
				return nil, err
			}
			n = int(nArg)
		}
		res := m.getGroup(n)
		return py.String(res), nil
	}
	return nil, py.ExceptionNewf(py.TypeError, "group() takes at most 1 argument, got %d", len(args))
}

func matchStart(self py.Object, args py.Tuple) (py.Object, error) {
	m, ok := self.(*Match)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected Match, got %s", self.Type().Name)
	}
	n := 0
	if len(args) > 0 {
		nArg, err := compat.AsInt(args[0], "start")
		if err != nil {
			return nil, err
		}
		n = int(nArg)
	}
	idx := m.getGroupIndex(n)
	if idx < 0 {
		return py.Int(-1), nil
	}
	return py.Int(idx), nil
}

func matchEnd(self py.Object, args py.Tuple) (py.Object, error) {
	m, ok := self.(*Match)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected Match, got %s", self.Type().Name)
	}
	n := 0
	if len(args) > 0 {
		nArg, err := compat.AsInt(args[0], "end")
		if err != nil {
			return nil, err
		}
		n = int(nArg)
	}
	idx := m.getGroupIndex(n + 1)
	if idx < 0 {
		return py.Int(-1), nil
	}
	return py.Int(idx), nil
}

func matchSpan(self py.Object, args py.Tuple) (py.Object, error) {
	m, ok := self.(*Match)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected Match, got %s", self.Type().Name)
	}
	n := 0
	if len(args) > 0 {
		nArg, err := compat.AsInt(args[0], "span")
		if err != nil {
			return nil, err
		}
		n = int(nArg)
	}
	start := m.getGroupIndex(n * 2)
	end := m.getGroupIndex(n*2 + 1)
	return py.Tuple{py.Int(start), py.Int(end)}, nil
}

// --- Internal helpers ---

// getGroup returns the substring for the given group number (0 = full match).
func (m *Match) getGroup(n int) string {
	startIdx := m.getGroupIndex(n * 2)
	endIdx := m.getGroupIndex(n*2 + 1)
	if startIdx < 0 || endIdx < 0 || startIdx >= endIdx {
		return ""
	}
	return m.text[startIdx:endIdx]
}

// getGroupIndex returns the match index from the indices slice, or -1 if out of range.
func (m *Match) getGroupIndex(idx int) int {
	if idx < 0 || idx >= len(m.indices) {
		return -1
	}
	return m.indices[idx]
}

// parseTwoArgs extracts two required positional arguments from args.
func parseTwoArgs(args py.Tuple, fn string) (py.Object, py.Object, error) {
	var a, b py.Object
	if err := py.UnpackTuple(args, nil, fn, 2, 2, &a, &b); err != nil {
		return nil, nil, err
	}
	return a, b, nil
}

// parseTwoOrThreeArgs extracts two required positional arguments and one
// optional int64 flags argument from args.
func parseTwoOrThreeArgs(args py.Tuple, fn string, flags *int64) (py.Object, py.Object, error) {
	var a, b, f py.Object
	if err := py.UnpackTuple(args, nil, fn, 2, 3, &a, &b, &f); err != nil {
		return nil, nil, err
	}
	if len(args) >= 3 {
		fl, err := compat.AsInt(f, fn)
		if err != nil {
			return nil, nil, err
		}
		*flags = fl
	}
	return a, b, nil
}

// parsePatternAndFlags extracts pattern and optional flags from args/kwargs.
func parsePatternAndFlags(args py.Tuple, kwargs py.StringDict, fn string, pattern, flags *py.Object) error {
	if len(args) >= 1 {
		*pattern = args[0]
	} else if v, ok := kwargs["pattern"]; ok {
		*pattern = v
	} else {
		return compat.RequiredArgError(fn, "pattern")
	}
	if len(args) >= 2 {
		*flags = args[1]
	} else if v, ok := kwargs["flags"]; ok {
		*flags = v
	}
	return nil
}

// compileFromObjects compiles a pattern from py.Object pattern and flags values.
func compileFromObjects(patternObj, flagsObj py.Object, fn string) (*Pattern, error) {
	patStr, err := compat.AsString(patternObj, fn)
	if err != nil {
		return nil, err
	}
	fl, err := compat.AsInt(flagsObj, fn)
	if err != nil {
		return nil, err
	}
	return compileRegexp(patStr, int(fl))
}

// compileRegexp compiles a pattern string with the given flags.
func compileRegexp(pat string, flags int) (*Pattern, error) {
	expr := pat
	if flags&reFlagIgnoreCase != 0 {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, py.ExceptionNewf(py.ValueError, "compile() failed: %v", err)
	}
	return &Pattern{re: re, pattern: pat, flags: flags}, nil
}

// toPattern converts a py.Object to a *Pattern. If the object is a string, it
// is compiled on the fly with the given flags. If it is already a Pattern,
// flags are ignored and the existing Pattern is returned as-is.
func toPattern(fn string, obj py.Object, flags int) (*Pattern, error) {
	switch v := obj.(type) {
	case *Pattern:
		return v, nil
	case py.String:
		return compileRegexp(string(v), flags)
	case py.Bytes:
		return compileRegexp(string(v), flags)
	default:
		return nil, py.ExceptionNewf(py.TypeError, "%s() pattern must be string or Pattern, not %s", fn, obj.Type().Name)
	}
}

// compileAndGetText compiles a pattern (with flags) and extracts text from objects.
func compileAndGetText(patObj, strObj py.Object, fn string, flags int) (*Pattern, string, error) {
	pp, err := toPattern(fn, patObj, flags)
	if err != nil {
		return nil, "", err
	}
	txt, err := compat.AsString(strObj, fn)
	if err != nil {
		return nil, "", err
	}
	return pp, txt, nil
}

// doSearchMatch performs search or match, returning Match or None.
func doSearchMatch(patObj, strObj py.Object, fn string, isSearch bool, flags int) (py.Object, error) {
	pp, txt, err := compileAndGetText(patObj, strObj, fn, flags)
	if err != nil {
		return nil, err
	}
	return searchMatchOn(pp, txt, isSearch)
}

// doSearchMatchOn performs search or match on an already-compiled Pattern.
func doSearchMatchOn(self py.Object, strObj py.Object, isSearch bool) (py.Object, error) {
	pp, ok := self.(*Pattern)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected Pattern, got %s", self.Type().Name)
	}
	txt, err := compat.AsString(strObj, "search")
	if err != nil {
		return nil, err
	}
	return searchMatchOn(pp, txt, isSearch)
}

// searchMatchOn performs the actual regex search/match.
// For match, only matches at the start of the string are returned.
func searchMatchOn(pp *Pattern, txt string, isSearch bool) (py.Object, error) {
	loc := pp.re.FindStringSubmatchIndex(txt)
	if loc == nil {
		return py.None, nil
	}
	if !isSearch && loc[0] != 0 {
		return py.None, nil
	}
	return &Match{pattern: pp, text: txt, indices: loc}, nil
}

// Ensure types implement py interfaces.
var (
	_ py.I__getattr__ = (*Pattern)(nil)
	_ py.I__getattr__ = (*Match)(nil)
	_ py.Object       = (*Pattern)(nil)
	_ py.Object       = (*Match)(nil)
)
