// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib

import (
	gojson "encoding/json"
	"strings"

	"github.com/pijalu/gpython/py"

	"github.com/pijalu/goa/internal/python/compat"
)

func init() {
	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "json",
			Doc:  "json — encode and decode JSON data",
		},
		Methods: []*py.Method{
			py.MustNewMethod("loads", jsonLoads, 0, `loads(s) -> object

Deserialize a JSON string to a Python object.`),
			py.MustNewMethod("dumps", jsonDumps, 0, `dumps(obj, indent=None) -> str

Serialize a Python object to a JSON string.`),
			py.MustNewMethod("load", jsonLoad, 0, `load(fp) -> object

Deserialize JSON read from a file-like object (via its read() method) to a
Python object.`),
			py.MustNewMethod("dump", jsonDump, 0, `dump(obj, fp, indent=None)

Serialize a Python object to JSON and write it to a file-like object (via its
write() method). Returns None.`),
		},
	})
}

func jsonLoads(self py.Object, args py.Tuple) (py.Object, error) {
	var s py.Object
	if err := py.UnpackTuple(args, nil, "loads", 1, 1, &s); err != nil {
		return nil, err
	}
	str, err := compat.AsString(s, "loads")
	if err != nil {
		return nil, err
	}
	var v any
	if err := gojson.Unmarshal([]byte(str), &v); err != nil {
		return nil, py.ExceptionNewf(py.ValueError, "loads() failed: %v", err)
	}
	return compat.GoToPy(v)
}

func jsonDumps(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	// Parse required argument
	var obj py.Object
	if err := py.UnpackTuple(args, nil, "dumps", 1, 1, &obj); err != nil {
		return nil, err
	}
	s, err := dumpsToString(obj, kwargs)
	if err != nil {
		return nil, err
	}
	return py.String(s), nil
}

// dumpsToString serializes obj to a JSON string, honoring the optional
// `indent` keyword (shared by dumps and dump).
func dumpsToString(obj py.Object, kwargs py.StringDict) (string, error) {
	// Parse optional indent keyword. CPython accepts str (used verbatim),
	// int (number of spaces; <=0 gives newlines only) and bool (acts as int,
	// since Python bool subclasses int).
	indent := ""
	indented := false
	if kwargs != nil {
		if v, ok := kwargs["indent"]; ok && v != py.None {
			s, err := indentValue(v)
			if err != nil {
				return "", err
			}
			indent = s
			indented = true
		}
	}

	// Convert Python object to Go value
	goVal, err := compat.PyToGo(obj)
	if err != nil {
		return "", py.ExceptionNewf(py.TypeError, "dumps() failed: %v", err)
	}

	// Serialize to JSON. MarshalIndent with an empty indent string still
	// emits newlines (CPython indent=0 semantics).
	var b []byte
	if indented {
		b, err = gojson.MarshalIndent(goVal, "", indent)
	} else {
		b, err = gojson.Marshal(goVal)
	}
	if err != nil {
		return "", py.ExceptionNewf(py.ValueError, "dumps() failed: %v", err)
	}
	return string(b), nil
}

// indentValue converts the indent keyword to the indent string: str values
// are used verbatim; int/bool values become that many spaces (clamped at 0).
// Any other type is a TypeError, matching CPython's "indent must be str, int
// or None".
func indentValue(v py.Object) (string, error) {
	if s, err := compat.AsString(v, "dumps"); err == nil {
		return s, nil
	}
	n, err := compat.AsInt(v, "dumps")
	if err != nil {
		return "", py.ExceptionNewf(py.TypeError, "dumps() indent must be str, int or None, not %s", v.Type().Name)
	}
	if n < 0 {
		n = 0
	}
	return strings.Repeat(" ", int(n)), nil
}

func jsonLoad(self py.Object, args py.Tuple) (py.Object, error) {
	var fp py.Object
	if err := py.UnpackTuple(args, nil, "load", 1, 1, &fp); err != nil {
		return nil, err
	}
	data, err := readFromFileObject(fp)
	if err != nil {
		return nil, err
	}
	var v any
	if err := gojson.Unmarshal([]byte(data), &v); err != nil {
		return nil, py.ExceptionNewf(py.ValueError, "load() failed: %v", err)
	}
	return compat.GoToPy(v)
}

func jsonDump(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	var obj, fp py.Object
	if err := py.UnpackTuple(args, nil, "dump", 2, 2, &obj, &fp); err != nil {
		return nil, err
	}
	s, err := dumpsToString(obj, kwargs)
	if err != nil {
		return nil, err
	}
	writeMeth, err := py.GetAttrString(fp, "write")
	if err != nil {
		return nil, py.ExceptionNewf(py.TypeError, "dump() argument 2 must have a write() method, not %s", fp.Type().Name)
	}
	if _, err := py.Call(writeMeth, py.Tuple{py.String(s)}, nil); err != nil {
		return nil, err
	}
	return py.None, nil
}

// readFromFileObject calls fp.read() and returns the content as a string
// (str or bytes results are both accepted, matching CPython's json.load).
func readFromFileObject(fp py.Object) (string, error) {
	readMeth, err := py.GetAttrString(fp, "read")
	if err != nil {
		return "", py.ExceptionNewf(py.TypeError, "load() argument 1 must have a read() method, not %s", fp.Type().Name)
	}
	data, err := py.Call(readMeth, py.Tuple{}, nil)
	if err != nil {
		return "", err
	}
	return compat.AsString(data, "load")
}
