// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib

import (
	goBase64 "encoding/base64"

	"github.com/pijalu/gpython/py"

	"github.com/pijalu/goa/internal/python/compat"
)

func init() {
	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "base64",
			Doc:  "base64 — base16, base32, base64, base85 data encodings",
		},
		Methods: []*py.Method{
			py.MustNewMethod("b64encode", base64Encode, 0, `b64encode(s) -> str

Encode a string or bytes using the standard Base64 alphabet and return a string.`),
			py.MustNewMethod("b64decode", base64Decode, 0, `b64decode(s) -> str

Decode a string using the standard Base64 alphabet and return a string.`),
			py.MustNewMethod("urlsafe_b64encode", base64URLEncode, 0, `urlsafe_b64encode(s) -> str

Encode a string or bytes using the URL-safe Base64 alphabet and return a string.`),
			py.MustNewMethod("urlsafe_b64decode", base64URLDecode, 0, `urlsafe_b64decode(s) -> str

Decode a string using the URL-safe Base64 alphabet and return a string.`),
		},
	})
}

func base64Encode(self py.Object, args py.Tuple) (py.Object, error) {
	var s py.Object
	if err := py.UnpackTuple(args, nil, "b64encode", 1, 1, &s); err != nil {
		return nil, err
	}
	b, err := compat.AsBytes(s, "b64encode")
	if err != nil {
		return nil, err
	}
	return py.String(goBase64.StdEncoding.EncodeToString(b)), nil
}

func base64Decode(self py.Object, args py.Tuple) (py.Object, error) {
	return base64DecodeWith(args, goBase64.StdEncoding, "b64decode")
}

func base64URLEncode(self py.Object, args py.Tuple) (py.Object, error) {
	var s py.Object
	if err := py.UnpackTuple(args, nil, "urlsafe_b64encode", 1, 1, &s); err != nil {
		return nil, err
	}
	b, err := compat.AsBytes(s, "urlsafe_b64encode")
	if err != nil {
		return nil, err
	}
	return py.String(goBase64.URLEncoding.EncodeToString(b)), nil
}

func base64URLDecode(self py.Object, args py.Tuple) (py.Object, error) {
	return base64DecodeWith(args, goBase64.URLEncoding, "urlsafe_b64decode")
}

func base64DecodeWith(args py.Tuple, enc *goBase64.Encoding, name string) (py.Object, error) {
	var s py.Object
	if err := py.UnpackTuple(args, nil, name, 1, 1, &s); err != nil {
		return nil, err
	}
	str, err := compat.AsString(s, name)
	if err != nil {
		return nil, err
	}
	b, err := enc.DecodeString(str)
	if err != nil {
		return nil, py.ExceptionNewf(py.ValueError, "%s() failed: %v", name, err)
	}
	return py.String(string(b)), nil
}
