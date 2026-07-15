// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib_test

import (
	"strings"
	"testing"
)

func TestUrllibParse(t *testing.T) {
	code := `
import urllib.parse
p = urllib.parse.urlparse("https://example.com/path?a=1#frag")
print(p.scheme)
print(p.netloc)
print(p.path)
print(p.query)
print(p.fragment)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"https", "example.com", "/path", "a=1", "frag"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

func TestUrllibQuote(t *testing.T) {
	code := `
import urllib.parse
print(urllib.parse.quote("a b"))
print(urllib.parse.quote("a/b", safe="/"))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "a%20b") {
		t.Errorf("expected a%%20b in output, got: %s", out)
	}
	if !strings.Contains(out, "a/b") {
		t.Errorf("expected a/b in output, got: %s", out)
	}
}

func TestUrllibQuotePlus(t *testing.T) {
	code := `
import urllib.parse
print(urllib.parse.quote_plus("a b"))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "a+b") {
		t.Errorf("expected a+b in output, got: %s", out)
	}
}

func TestUrllibUnquote(t *testing.T) {
	code := `
import urllib.parse
print(urllib.parse.unquote("a%20b"))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "a b") {
		t.Errorf("expected a b in output, got: %s", out)
	}
}

func TestUrllibUnquotePlus(t *testing.T) {
	code := `
import urllib.parse
print(urllib.parse.unquote_plus("a+b"))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "a b") {
		t.Errorf("expected a b in output, got: %s", out)
	}
}

func TestUrllibUrlencode(t *testing.T) {
	code := `
import urllib.parse
print(urllib.parse.urlencode({"a": "1", "b": "2"}))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "a=1") || !strings.Contains(out, "b=2") {
		t.Errorf("expected a=1 and b=2 in output, got: %s", out)
	}
}

func TestUrllibParseQS(t *testing.T) {
	code := `
import urllib.parse
qs = urllib.parse.parse_qs("a=1&a=2&b=3")
print(len(qs["a"]))
print(qs["b"][0])
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "2") || !strings.Contains(out, "3") {
		t.Errorf("expected 2 and 3 in output, got: %s", out)
	}
}

func TestUrllibParseQSL(t *testing.T) {
	code := `
import urllib.parse
qsl = urllib.parse.parse_qsl("a=1&b=2")
print(len(qsl))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "2") {
		t.Errorf("expected 2 in output, got: %s", out)
	}
}

func TestUrllibUrlunparse(t *testing.T) {
	code := `
import urllib.parse
print(urllib.parse.urlunparse(["https", "example.com", "/path", "", "a=1", "frag"]))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "https://example.com/path?a=1#frag") {
		t.Errorf("expected reconstructed URL in output, got: %s", out)
	}
}

func TestUrllibUrlencodeList(t *testing.T) {
	code := `
import urllib.parse
print(urllib.parse.urlencode({"a": ["1", "2"]}))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "a=1") || !strings.Contains(out, "a=2") {
		t.Errorf("expected a=1 and a=2 in output, got: %s", out)
	}
}

func TestUrllibUrlencodeTuple(t *testing.T) {
	code := `
import urllib.parse
print(urllib.parse.urlencode({"a": ("1", "2")}))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "a=1") || !strings.Contains(out, "a=2") {
		t.Errorf("expected a=1 and a=2 in output, got: %s", out)
	}
}

func TestUrllibUnquoteInvalid(t *testing.T) {
	code := `
import urllib.parse
try:
    urllib.parse.unquote("%")
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

func TestUrllibParseInvalid(t *testing.T) {
	code := `
import urllib.parse
try:
    urllib.parse.urlparse("://bad")
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
