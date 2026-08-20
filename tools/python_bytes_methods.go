// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/pijalu/gpython/py"
)

// pyBytesMethodsOnce guards the one-time registration of methods the embedded
// gpython py.BytesType omits. gpython's own init registers only "replace" on
// BytesType.Dict; this adds the common bytes.decode so the idiomatic
// byte-oriented read `open(path, 'rb').read().decode('utf-8')` runs unmodified.
// Registration is idempotent and patches the single global py.BytesType, so it
// is available to every interpreter context.
var pyBytesMethodsOnce sync.Once

// registerPyBytesMethods patches the global py.BytesType with the bytes-object
// methods gpython omits. It is safe to call from any interpreter setup path.
func registerPyBytesMethods() {
	pyBytesMethodsOnce.Do(func() {
		py.BytesType.Dict["decode"] = py.MustNewMethod("decode",
			func(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
				return bytesDecode(self, args, kwargs)
			}, 0, bytesDecodeDoc)
	})
}

// bytesDecode implements bytes.decode(encoding='utf-8', errors='strict') with
// CPython semantics for the UTF-8 family: 'strict' raises UnicodeDecodeError on
// the first invalid sequence, 'ignore' drops invalid bytes, and 'replace'
// substitutes U+FFFD for each invalid byte. Unknown encodings raise
// LookupError, matching CPython.
func bytesDecode(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	b, ok := self.(py.Bytes)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError,
			"decode() requires a bytes object, not %s", self.Type().Name)
	}
	var encoding py.Object = py.String("utf-8")
	var errors py.Object = py.String("strict")
	if err := py.UnpackTuple(args, kwargs, "decode", 0, 2, &encoding, &errors); err != nil {
		return nil, err
	}
	enc, err := bytesDecodeStr(encoding, "encoding")
	if err != nil {
		return nil, err
	}
	errMode, err := bytesDecodeStr(errors, "errors")
	if err != nil {
		return nil, err
	}
	if !isUTF8Encoding(enc) {
		return nil, py.ExceptionNewf(py.LookupError, "unknown encoding: %s", enc)
	}
	return decodeUTF8([]byte(b), enc, errMode)
}

// bytesDecodeStr coerces an encoding/errors argument to a lowercase Go string;
// a non-str argument raises TypeError like CPython's decode.
func bytesDecodeStr(o py.Object, name string) (string, error) {
	s, ok := o.(py.String)
	if !ok {
		return "", py.ExceptionNewf(py.TypeError,
			"decode() argument '%s' must be str, not %s", name, o.Type().Name)
	}
	return strings.ToLower(string(s)), nil
}

// isUTF8Encoding reports whether the (lowercased) encoding name selects the
// UTF-8 codec, accepting CPython's common aliases.
func isUTF8Encoding(enc string) bool {
	switch enc {
	case "utf-8", "utf8", "utf_8", "u8", "utf", "cp65001":
		return true
	}
	return false
}

// decodeUTF8 decodes b as UTF-8 honoring the errors mode. For "strict" it
// returns a UnicodeDecodeError at the first invalid byte; for "ignore" it drops
// each invalid byte; for "replace" it emits one U+FFFD per invalid byte. An
// unknown errors mode raises LookupError like CPython.
func decodeUTF8(b []byte, enc, errMode string) (py.String, error) {
	switch errMode {
	case "strict", "ignore", "replace":
	default:
		return "", py.ExceptionNewf(py.LookupError,
			"unknown error handler name '%s'", errMode)
	}

	var sb strings.Builder
	sb.Grow(len(b))
	for i := 0; i < len(b); {
		if b[i] < utf8.RuneSelf { // fast path: ASCII byte
			sb.WriteByte(b[i])
			i++
			continue
		}
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size == 1 { // invalid UTF-8 byte at i
			if errMode == "strict" {
				return "", py.ExceptionNewf(py.UnicodeDecodeError,
					"'%s' codec can't decode byte 0x%02x in position %d: invalid start byte",
					enc, b[i], i)
			}
			if errMode == "replace" {
				sb.WriteRune(utf8.RuneError)
			}
			i++
			continue
		}
		sb.WriteRune(r)
		i += size
	}
	return py.String(sb.String()), nil
}

const bytesDecodeDoc = `decode(encoding='utf-8', errors='strict') -> str

Decode the bytes using the codec registered for encoding. encoding defaults
to 'utf-8'. errors may be 'strict' (raise UnicodeDecodeError), 'ignore'
(skip invalid bytes), or 'replace' (substitute U+FFFD). Only the UTF-8 codec
family is supported; any other encoding raises LookupError.`

func init() {
	registerPyBytesMethods()
}
