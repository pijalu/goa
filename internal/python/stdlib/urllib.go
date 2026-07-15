// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/go-python/gpython/py"

	"github.com/pijalu/goa/internal/python/compat"
)

// ParseResult is the object returned by urlparse.
type ParseResult struct {
	scheme   string
	netloc   string
	path     string
	params   string
	query    string
	fragment string
}

var (
	parseResultType = py.NewType("ParseResult", `ParseResult

Result of urlparse().`)

	urllibParseImpl = &py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "urllib.parse",
			Doc:  "urllib.parse — parse URLs into components",
		},
		Globals: py.StringDict{},
		Methods: []*py.Method{
			py.MustNewMethod("quote", urllibQuote, 0, `quote(s, safe='') -> str

Quote special characters in a string using %xx escapes.`),
			py.MustNewMethod("quote_plus", urllibQuotePlus, 0, `quote_plus(s, safe='') -> str

Like quote(), but also replace spaces with plus signs.`),
			py.MustNewMethod("unquote", urllibUnquote, 0, `unquote(s) -> str

Replace %xx escapes by their single-character equivalent.`),
			py.MustNewMethod("unquote_plus", urllibUnquotePlus, 0, `unquote_plus(s) -> str

Like unquote(), but also replace plus signs with spaces.`),
			py.MustNewMethod("urlencode", urllibUrlencode, 0, `urlencode(query) -> str

Encode a sequence of key/value pairs into a query string.`),
			py.MustNewMethod("urlparse", urllibParse, 0, `urlparse(url) -> ParseResult

Parse a URL into six components.`),
			py.MustNewMethod("urlunparse", urllibUnparse, 0, `urlunparse(parts) -> str

Put a URL back together from its components.`),
			py.MustNewMethod("parse_qs", urllibParseQS, 0, `parse_qs(query) -> dict

Parse a query string into a dict mapping names to lists of values.`),
			py.MustNewMethod("parse_qsl", urllibParseQSL, 0, `parse_qsl(query) -> list

Parse a query string into a list of (name, value) pairs.`),
		},
	}
)

func init() {
	py.RegisterModule(urllibParseImpl)

	// Build a static urllib.parse module object that can be attached as the
	// "parse" attribute of the top-level urllib package. This makes
	// "import urllib.parse; urllib.parse.quote(...)" work because Python
	// binds the top-level package (urllib) to the name "urllib" and expects
	// "parse" to be an attribute on it.
	parseModule := &py.Module{
		ModuleImpl: urllibParseImpl,
		Globals:    urllibParseImpl.Globals.Copy(),
	}
	for _, method := range urllibParseImpl.Methods {
		methodInst := new(py.Method)
		*methodInst = *method
		methodInst.Module = parseModule
		parseModule.Globals[method.Name] = methodInst
	}
	parseModule.Globals["__name__"] = py.String("urllib.parse")
	parseModule.Globals["__doc__"] = py.String(urllibParseImpl.Info.Doc)
	parseModule.Globals["__package__"] = py.None

	// Self-reference so that the dotted-import form returns a module with a
	// "parse" attribute that still exposes the same functions.
	urllibParseImpl.Globals["parse"] = parseModule

	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "urllib",
			Doc:  "urllib — URL handling modules",
		},
		Globals: py.StringDict{
			"parse": parseModule,
		},
	})
}

// Type returns the ParseResult type.
func (p *ParseResult) Type() *py.Type { return parseResultType }

// M__getattr__ exposes URL components as read-only attributes.
func (p *ParseResult) M__getattr__(name string) (py.Object, error) {
	switch name {
	case "scheme":
		return py.String(p.scheme), nil
	case "netloc":
		return py.String(p.netloc), nil
	case "path":
		return py.String(p.path), nil
	case "params":
		return py.String(p.params), nil
	case "query":
		return py.String(p.query), nil
	case "fragment":
		return py.String(p.fragment), nil
	}
	return nil, py.ExceptionNewf(py.AttributeError, "'ParseResult' object has no attribute '%s'", name)
}

func urllibQuote(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	s, safe, err := parseQuoteArgs(args, kwargs, "quote")
	if err != nil {
		return nil, err
	}
	return py.String(quoteString(s, safe, false)), nil
}

func urllibQuotePlus(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	s, safe, err := parseQuoteArgs(args, kwargs, "quote_plus")
	if err != nil {
		return nil, err
	}
	return py.String(quoteString(s, safe, true)), nil
}

func parseQuoteArgs(args py.Tuple, kwargs py.StringDict, name string) (string, string, error) {
	var s, safe py.Object
	if err := py.UnpackTuple(args, nil, name, 1, 2, &s, &safe); err != nil {
		return "", "", err
	}
	if safe == nil && kwargs != nil {
		if v, ok := kwargs["safe"]; ok {
			safe = v
		}
	}
	if safe == nil {
		safe = py.String("")
	}
	str, err := compat.AsString(s, name)
	if err != nil {
		return "", "", err
	}
	safeStr, err := compat.AsString(safe, name)
	if err != nil {
		return "", "", err
	}
	return str, safeStr, nil
}

func urllibUnquote(self py.Object, args py.Tuple) (py.Object, error) {
	var s py.Object
	if err := py.UnpackTuple(args, nil, "unquote", 1, 1, &s); err != nil {
		return nil, err
	}
	str, err := compat.AsString(s, "unquote")
	if err != nil {
		return nil, err
	}
	res, err := url.PathUnescape(str)
	if err != nil {
		return nil, py.ExceptionNewf(py.ValueError, "unquote() failed: %v", err)
	}
	return py.String(res), nil
}

func urllibUnquotePlus(self py.Object, args py.Tuple) (py.Object, error) {
	var s py.Object
	if err := py.UnpackTuple(args, nil, "unquote_plus", 1, 1, &s); err != nil {
		return nil, err
	}
	str, err := compat.AsString(s, "unquote_plus")
	if err != nil {
		return nil, err
	}
	res, err := url.QueryUnescape(strings.ReplaceAll(str, "+", " "))
	if err != nil {
		return nil, py.ExceptionNewf(py.ValueError, "unquote_plus() failed: %v", err)
	}
	return py.String(res), nil
}

func urllibUrlencode(self py.Object, args py.Tuple) (py.Object, error) {
	var query py.Object
	if err := py.UnpackTuple(args, nil, "urlencode", 1, 1, &query); err != nil {
		return nil, err
	}
	d, ok := query.(py.StringDict)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "urlencode() argument must be dict, not %s", query.Type().Name)
	}
	values := url.Values{}
	for k, v := range d {
		if err := addDictValue(values, k, v); err != nil {
			return nil, err
		}
	}
	return py.String(values.Encode()), nil
}

func addDictValue(values url.Values, k string, v py.Object) error {
	switch val := v.(type) {
	case py.String:
		values.Add(k, string(val))
	case *py.List:
		for _, item := range val.Items {
			itemStr, err := compat.AsString(item, "urlencode")
			if err != nil {
				return err
			}
			values.Add(k, itemStr)
		}
	case py.Tuple:
		for _, item := range val {
			itemStr, err := compat.AsString(item, "urlencode")
			if err != nil {
				return err
			}
			values.Add(k, itemStr)
		}
	default:
		itemStr, err := compat.AsString(v, "urlencode")
		if err != nil {
			return err
		}
		values.Add(k, itemStr)
	}
	return nil
}

func urllibParse(self py.Object, args py.Tuple) (py.Object, error) {
	var s py.Object
	if err := py.UnpackTuple(args, nil, "urlparse", 1, 1, &s); err != nil {
		return nil, err
	}
	str, err := compat.AsString(s, "urlparse")
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(str)
	if err != nil {
		return nil, py.ExceptionNewf(py.ValueError, "urlparse() failed: %v", err)
	}
	return parseResultFromURL(u), nil
}

func urllibUnparse(self py.Object, args py.Tuple) (py.Object, error) {
	var parts py.Object
	if err := py.UnpackTuple(args, nil, "urlunparse", 1, 1, &parts); err != nil {
		return nil, err
	}
	items, err := compat.IterItems(parts)
	if err != nil {
		return nil, err
	}
	if len(items) != 6 {
		return nil, py.ExceptionNewf(py.ValueError, "urlunparse() expects a 6-element sequence")
	}
	strs := make([]string, 6)
	for i, item := range items {
		s, err := compat.AsString(item, "urlunparse")
		if err != nil {
			return nil, err
		}
		strs[i] = s
	}
	u := &url.URL{
		Scheme:   strs[0],
		Host:     strs[1],
		Path:     strs[2],
		RawQuery: strs[4],
		Fragment: strs[5],
	}
	return py.String(u.String()), nil
}

func urllibParseQS(self py.Object, args py.Tuple) (py.Object, error) {
	var s py.Object
	if err := py.UnpackTuple(args, nil, "parse_qs", 1, 1, &s); err != nil {
		return nil, err
	}
	str, err := compat.AsString(s, "parse_qs")
	if err != nil {
		return nil, err
	}
	values, err := url.ParseQuery(str)
	if err != nil {
		return nil, py.ExceptionNewf(py.ValueError, "parse_qs() failed: %v", err)
	}
	d := py.NewStringDict()
	for k, vals := range values {
		items := make([]py.Object, len(vals))
		for i, v := range vals {
			items[i] = py.String(v)
		}
		d[k] = py.NewListFromItems(items)
	}
	return d, nil
}

func urllibParseQSL(self py.Object, args py.Tuple) (py.Object, error) {
	var s py.Object
	if err := py.UnpackTuple(args, nil, "parse_qsl", 1, 1, &s); err != nil {
		return nil, err
	}
	str, err := compat.AsString(s, "parse_qsl")
	if err != nil {
		return nil, err
	}
	values, err := url.ParseQuery(str)
	if err != nil {
		return nil, py.ExceptionNewf(py.ValueError, "parse_qsl() failed: %v", err)
	}
	var result []py.Object
	for k, vals := range values {
		for _, v := range vals {
			result = append(result, py.Tuple{py.String(k), py.String(v)})
		}
	}
	return py.NewListFromItems(result), nil
}

func parseResultFromURL(u *url.URL) *ParseResult {
	return &ParseResult{
		scheme:   u.Scheme,
		netloc:   u.Host,
		path:     u.Path,
		params:   "",
		query:    u.RawQuery,
		fragment: u.Fragment,
	}
}

// quoteString percent-encodes s. Characters in safe and the always-safe set
// [-_.~] are preserved. If plusForSpace is true, spaces are replaced with '+'.
func quoteString(s, safe string, plusForSpace bool) string {
	safeSet := make(map[byte]bool, len(safe)+4)
	for _, c := range []byte("-_.~") {
		safeSet[c] = true
	}
	for i := 0; i < len(safe); i++ {
		safeSet[safe[i]] = true
	}
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreserved(c) || safeSet[c] {
			out.WriteByte(c)
		} else if plusForSpace && c == ' ' {
			out.WriteByte('+')
		} else {
			out.WriteString(fmt.Sprintf("%%%02X", c))
		}
	}
	return out.String()
}

func isUnreserved(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// Ensure ParseResult implements py.Object and __getattr__.
var (
	_ py.I__getattr__ = (*ParseResult)(nil)
	_ py.Object        = (*ParseResult)(nil)
)
