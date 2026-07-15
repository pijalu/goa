// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package compat provides shared helpers for Go-backed gpython stdlib modules.
package compat

import (
	"reflect"

	"github.com/go-python/gpython/py"
)

// AsString converts a py.Object to a Go string. It accepts py.String and py.Bytes.
func AsString(o py.Object, fn string) (string, error) {
	switch v := o.(type) {
	case py.String:
		return string(v), nil
	case py.Bytes:
		return string(v), nil
	}
	return "", py.ExceptionNewf(py.TypeError, "%s() argument must be str, not %s", fn, o.Type().Name)
}

// AsStringOrDefault converts o to a string when it is a string or bytes, otherwise returns def.
func AsStringOrDefault(o py.Object, def string) string {
	switch v := o.(type) {
	case py.String:
		return string(v)
	case py.Bytes:
		return string(v)
	}
	return def
}

// AsBytes converts a py.Object to a Go byte slice. It accepts py.String and py.Bytes.
func AsBytes(o py.Object, fn string) ([]byte, error) {
	switch v := o.(type) {
	case py.String:
		return []byte(v), nil
	case py.Bytes:
		return []byte(v), nil
	}
	return nil, py.ExceptionNewf(py.TypeError, "%s() argument must be str or bytes, not %s", fn, o.Type().Name)
}

// AsInt converts a py.Object to an int64.
func AsInt(o py.Object, fn string) (int64, error) {
	switch v := o.(type) {
	case py.Int:
		return int64(v), nil
	case py.Bool:
		if v {
			return 1, nil
		}
		return 0, nil
	}
	return 0, py.ExceptionNewf(py.TypeError, "%s() argument must be int, not %s", fn, o.Type().Name)
}

// AsIntOrDefault converts o to an int64 or returns def.
func AsIntOrDefault(o py.Object, def int64) int64 {
	switch v := o.(type) {
	case py.Int:
		return int64(v)
	case py.Bool:
		if v {
			return 1
		}
		return 0
	}
	return def
}

// AsBool converts a py.Object to a bool. Non-bool objects are false.
func AsBool(o py.Object) bool {
	if b, ok := o.(py.Bool); ok {
		return bool(b)
	}
	return false
}

// AsFloat converts a py.Object to a float64.
func AsFloat(o py.Object, fn string) (float64, error) {
	switch v := o.(type) {
	case py.Float:
		return float64(v), nil
	case py.Int:
		return float64(int64(v)), nil
	}
	return 0, py.ExceptionNewf(py.TypeError, "%s() argument must be number, not %s", fn, o.Type().Name)
}

// PyToGo converts a supported py.Object to a Go value. It supports None, bool, int,
// float, string, bytes, list, tuple, and dict with string keys.
func PyToGo(o py.Object) (any, error) {
	switch v := o.(type) {
	case nil:
		return nil, nil
	case py.NoneType:
		return nil, nil
	case py.Bool:
		return bool(v), nil
	case py.Int:
		return int64(v), nil
	case py.Float:
		return float64(v), nil
	case py.String:
		return string(v), nil
	case py.Bytes:
		return []byte(v), nil
	case *py.List:
		return pySequenceToGo(v.Items)
	case py.Tuple:
		return pySequenceToGo(v)
	case py.StringDict:
		return pyDictToGo(v)
	}
	return nil, py.ExceptionNewf(py.TypeError, "unsupported Python type: %s", o.Type().Name)
}

func pySequenceToGo(items []py.Object) ([]any, error) {
	result := make([]any, len(items))
	for i, item := range items {
		val, err := PyToGo(item)
		if err != nil {
			return nil, err
		}
		result[i] = val
	}
	return result, nil
}

func pyDictToGo(d py.StringDict) (map[string]any, error) {
	m := make(map[string]any, len(d))
	for k, val := range d {
		gv, err := PyToGo(val)
		if err != nil {
			return nil, err
		}
		m[k] = gv
	}
	return m, nil
}

// GoToPy converts a Go value to a py.Object. It supports nil, bool, int types,
// float64, string, []byte, []any, and map[string]any.
func GoToPy(v any) (py.Object, error) {
	if v == nil {
		return py.None, nil
	}
	switch val := v.(type) {
	case bool:
		return py.Bool(val), nil
	case string:
		return py.String(val), nil
	case []byte:
		return py.Bytes(val), nil
	case []any:
		return goSliceToPy(val)
	case map[string]any:
		return goMapToPy(val)
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return py.Int(rv.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return py.Int(int64(rv.Uint())), nil
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		if f == float64(int64(f)) {
			return py.Int(f), nil
		}
		return py.Float(f), nil
	}
	return nil, py.ExceptionNewf(py.TypeError, "unsupported Go type: %T", v)
}

func goSliceToPy(items []any) (py.Object, error) {
	objs := make([]py.Object, len(items))
	for i, item := range items {
		po, err := GoToPy(item)
		if err != nil {
			return nil, err
		}
		objs[i] = po
	}
	return py.NewListFromItems(objs), nil
}

func goMapToPy(m map[string]any) (py.Object, error) {
	d := py.NewStringDict()
	for k, val := range m {
		po, err := GoToPy(val)
		if err != nil {
			return nil, err
		}
		d[k] = po
	}
	return d, nil
}

// FormatError returns a TypeError for the given function and argument.
func FormatError(fn, expected string, o py.Object) error {
	return py.ExceptionNewf(py.TypeError, "%s() argument must be %s, not %s", fn, expected, o.Type().Name)
}

// RequiredArgError returns a TypeError for a missing required argument.
func RequiredArgError(fn, name string) error {
	return py.ExceptionNewf(py.TypeError, "%s() missing required argument: %s", fn, name)
}

// TooFewArgsError returns a TypeError for too few arguments.
func TooFewArgsError(fn string, min int) error {
	return py.ExceptionNewf(py.TypeError, "%s() takes at least %d arguments", fn, min)
}

// TooManyArgsError returns a TypeError for too many arguments.
func TooManyArgsError(fn string, max int) error {
	return py.ExceptionNewf(py.TypeError, "%s() takes at most %d arguments", fn, max)
}

// MustString returns a TypeError if o is not a string or bytes.
func MustString(o py.Object, fn string) error {
	if _, ok := o.(py.String); ok {
		return nil
	}
	if _, ok := o.(py.Bytes); ok {
		return nil
	}
	return FormatError(fn, "str", o)
}

// MustInt returns a TypeError if o is not an int.
func MustInt(o py.Object, fn string) error {
	if _, ok := o.(py.Int); ok {
		return nil
	}
	return FormatError(fn, "int", o)
}

// PyObjectOrNone returns o if it is not nil, otherwise py.None.
func PyObjectOrNone(o py.Object) py.Object {
	if o == nil {
		return py.None
	}
	return o
}

// IterItems returns the items of a Python sequence as a slice of py.Object.
func IterItems(o py.Object) ([]py.Object, error) {
	switch v := o.(type) {
	case *py.List:
		return v.Items, nil
	case py.Tuple:
		return []py.Object(v), nil
	case py.String:
		items := make([]py.Object, len(v))
		for i, r := range v {
			items[i] = py.String(r)
		}
		return items, nil
	case py.Bytes:
		items := make([]py.Object, len(v))
		for i, b := range v {
			items[i] = py.Int(b)
		}
		return items, nil
	}
	return nil, py.ExceptionNewf(py.TypeError, "object is not iterable: %s", o.Type().Name)
}

// NewPyList builds a py.List from a slice of Go values.
func NewPyList(items []any) (py.Object, error) {
	objs := make([]py.Object, len(items))
	for i, item := range items {
		po, err := GoToPy(item)
		if err != nil {
			return nil, err
		}
		objs[i] = po
	}
	return py.NewListFromItems(objs), nil
}

// NewPyStringDict builds a py.StringDict from a Go map.
func NewPyStringDict(m map[string]any) (py.StringDict, error) {
	d := py.NewStringDict()
	for k, v := range m {
		po, err := GoToPy(v)
		if err != nil {
			return nil, err
		}
		d[k] = po
	}
	return d, nil
}

// Ensure these types satisfy py.Object at compile time.
var (
	_ py.Object = py.String("")
	_ py.Object = py.Bytes(nil)
	_ py.Object = py.Int(0)
	_ py.Object = py.Float(0)
	_ py.Object = py.Bool(false)
	_ py.Object = py.None
	_ py.Object = py.Tuple{}
)
