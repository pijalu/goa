// SPDX-License-Identifier: GPL-3.0-or-later

package stdlib

import (
	"regexp"
	"strings"

	"github.com/pijalu/goa/internal/python/compat"
	"github.com/pijalu/gpython/py"
)

// expandTemplateByte writes the expansion of the template unit starting at
// tpl[i] (a literal byte or a backslash escape) and returns how many extra
// template bytes it consumed beyond the current one.
func expandTemplateByte(pp *Pattern, m *Match, tpl string, i int, b *strings.Builder) (int, error) {
	c := tpl[i]
	if c != '\\' || i+1 >= len(tpl) {
		// A trailing backslash is kept verbatim (RE2-subset pragmatism;
		// CPython raises 'bad escape (end of pattern)').
		b.WriteByte(c)
		return 0, nil
	}
	switch n := tpl[i+1]; {
	case n == '\\':
		b.WriteByte('\\')
		return 1, nil
	case n == 'g':
		consumed, err := expandGReference(pp, m, tpl, i, b)
		if err != nil {
			return 0, err
		}
		return consumed, nil
	case n >= '0' && n <= '9':
		consumed, err := expandNumericReference(pp, m, tpl, i, b)
		if err != nil {
			return 0, err
		}
		return consumed, nil
	case isASCIILetter(n):
		return 0, py.ExceptionNewf(py.ValueError,
			"bad escape \\%c at position %d", n, i)
	default:
		// Non-letter, non-digit escapes (e.g. \n, \t) are kept verbatim,
		// matching CPython.
		b.WriteByte('\\')
		b.WriteByte(n)
		return 1, nil
	}
}

// expandNumericReference expands a \N or \NN group reference at tpl[i]
// (i points at the backslash) and returns the bytes consumed after it.
func expandNumericReference(pp *Pattern, m *Match, tpl string, i int, b *strings.Builder) (int, error) {
	num := int(tpl[i+1] - '0')
	used := 1
	if i+2 < len(tpl) && tpl[i+2] >= '0' && tpl[i+2] <= '9' {
		num = num*10 + int(tpl[i+2]-'0')
		used = 2
	}
	if err := writeTemplateGroup(pp, m, num, b); err != nil {
		return 0, err
	}
	return used, nil
}

// expandGReference expands a \g<N> or \g<name> group reference starting at
// the backslash at tpl[i]. It writes the referenced group text to b and
// returns the number of template bytes consumed after the backslash
// ("g<N>"), mirroring CPython's error wording for malformed references.
func expandGReference(pp *Pattern, m *Match, tpl string, i int, b *strings.Builder) (int, error) {
	if i+2 >= len(tpl) || tpl[i+2] != '<' {
		return 0, py.ExceptionNewf(py.ValueError, "bad escape \\g at position %d", i)
	}
	close := strings.IndexByte(tpl[i+3:], '>')
	if close < 0 {
		return 0, py.ExceptionNewf(py.ValueError, "bad escape \\g at position %d", i)
	}
	name := tpl[i+3 : i+3+close]
	if name == "" {
		return 0, py.ExceptionNewf(py.ValueError, "missing group name at position %d", i+3)
	}
	if isASCIIAllDigits(name) {
		num := 0
		for _, d := range name {
			num = num*10 + int(d-'0')
		}
		if err := writeTemplateGroup(pp, m, num, b); err != nil {
			return 0, err
		}
		return 2 + close + 1, nil
	}
	num := groupIndexByName(pp, name)
	if num < 0 {
		return 0, py.ExceptionNewf(py.IndexError,
			"unknown group name '%s' at position %d", name, i+3)
	}
	if err := writeTemplateGroup(pp, m, num, b); err != nil {
		return 0, err
	}
	return 2 + close + 1, nil
}

// writeTemplateGroup writes the text of group num for match m to b,
// validating the reference the way CPython does. Group 0 refers to the
// whole match; an unmatched optional group expands to the empty string.
func writeTemplateGroup(pp *Pattern, m *Match, num int, b *strings.Builder) error {
	if num < 0 || num > pp.re.NumSubexp() {
		return py.ExceptionNewf(py.IndexError,
			"invalid group reference %d at position 1", num)
	}
	b.WriteString(m.getGroup(num))
	return nil
}

// groupIndexByName returns the 1-based group index for a named group, or -1.
func groupIndexByName(pp *Pattern, name string) int {
	for i, n := range pp.re.SubexpNames() {
		if i > 0 && n == name {
			return i
		}
	}
	return -1
}

// isASCIILetter reports whether c is an ASCII letter.
func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isASCIIAllDigits reports whether s is non-empty ASCII digits only.
func isASCIIAllDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
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
	orig := pat
	if flags&reFlagVerbose != 0 {
		pat = stripVerbosePattern(pat)
	}
	var prefix strings.Builder
	if flags&(reFlagIgnoreCase|reFlagDotAll|reFlagMultiline) != 0 {
		prefix.WriteString("(?")
		if flags&reFlagIgnoreCase != 0 {
			prefix.WriteByte('i')
		}
		if flags&reFlagDotAll != 0 {
			prefix.WriteByte('s')
		}
		if flags&reFlagMultiline != 0 {
			prefix.WriteByte('m')
		}
		prefix.WriteString(")")
	}
	re, err := regexp.Compile(prefix.String() + pat)
	if err != nil {
		return nil, py.ExceptionNewf(py.ValueError, "compile() failed: %v", err)
	}
	return &Pattern{re: re, pattern: orig, flags: flags}, nil
}

// stripVerbosePattern implements re.VERBOSE on RE2 (which has no (?x)):
// unescaped whitespace and #-to-end-of-line comments are removed everywhere
// except inside character classes. Escaped characters are always kept
// verbatim, so `\ ` and `\#` stay literal.
func stripVerbosePattern(pat string) string {
	var b strings.Builder
	b.Grow(len(pat))
	inClass := false
	escaped := false
	for i := 0; i < len(pat); i++ {
		c := pat[i]
		switch {
		case escaped:
			b.WriteByte(c)
			escaped = false
		case c == '\\':
			b.WriteByte(c)
			escaped = true
		case inClass:
			b.WriteByte(c)
			inClass = c != ']'
		case c == '[':
			b.WriteByte(c)
			inClass = true
		case c == '#':
			i = skipVerboseComment(pat, i)
		case isVerboseSpace(c):
			// drop unescaped whitespace
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// isVerboseSpace reports whether c is unescaped whitespace dropped by
// re.VERBOSE.
func isVerboseSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

// skipVerboseComment returns the index of the last byte of the #-comment
// starting at i — the byte before the terminating newline, or the end of the
// pattern.
func skipVerboseComment(pat string, i int) int {
	for i+1 < len(pat) && pat[i+1] != '\n' {
		i++
	}
	return i
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
