// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib

import (
	gojson "encoding/json"

	"github.com/go-python/gpython/py"

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

	// Parse optional indent keyword
	indent := ""
	if kwargs != nil {
		if v, ok := kwargs["indent"]; ok && v != py.None {
			indentStr, err := compat.AsString(v, "dumps")
			if err != nil {
				return nil, py.ExceptionNewf(py.TypeError, "dumps() indent must be str or None, not %s", v.Type().Name)
			}
			indent = indentStr
		}
	}

	// Convert Python object to Go value
	goVal, err := compat.PyToGo(obj)
	if err != nil {
		return nil, py.ExceptionNewf(py.TypeError, "dumps() failed: %v", err)
	}

	// Serialize to JSON
	var b []byte
	if indent != "" {
		b, err = gojson.MarshalIndent(goVal, "", indent)
	} else {
		b, err = gojson.Marshal(goVal)
	}
	if err != nil {
		return nil, py.ExceptionNewf(py.ValueError, "dumps() failed: %v", err)
	}

	return py.String(b), nil
}
