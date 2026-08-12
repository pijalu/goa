// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"sync"

	"github.com/pijalu/gpython/py"
)

// pyDictMethodsOnce guards the one-time replacement of gpython's
// py.StringDictType "get" method. Upstream gpython restricts dict keys to
// strings; its get() raises KeyError for any non-string key and ignores the
// default argument entirely on that path, so the idiomatic tally
// `by_status.get(st, 0)` with an int status code crashed. Registration is
// idempotent and patches the single global py.StringDictType, so it is
// available to every interpreter context.
//
// Note: d[key] = value / key in d / d[key] cannot be shimmed the same way —
// py.SetItem and py.SequenceContains dispatch to the native Go interface
// methods M__setitem__ / M__contains__ on py.StringDict before ever
// consulting the type's method table, so changing them requires forking
// gpython (tracked as a follow-up in).
var pyDictMethodsOnce sync.Once

// registerPyDictMethods patches the global py.StringDictType with a
// CPython-compatible dict.get. It is safe to call from any interpreter setup
// path.
func registerPyDictMethods() {
	pyDictMethodsOnce.Do(func() {
		py.StringDictType.Dict["get"] = py.MustNewMethod("get",
			func(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
				return dictGet(self, args, kwargs)
			}, 0, dictGetDoc)
	})
}

// dictGet implements dict.get(key[, default]) with CPython semantics adapted
// to gpython's string-keys-only StringDict: a str key is looked up directly;
// any other key type is normalized through str(key) (so int 200 and str
// "200" address the same bucket, matching how such values must be stored);
// a missing key returns default, or None when default is not given.
func dictGet(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	sMap, ok := self.(py.StringDict)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError,
			"get() requires a dict object, not %s", self.Type().Name)
	}
	var key py.Object
	var def py.Object = py.None
	if err := py.UnpackTuple(args, kwargs, "get", 1, 2, &key, &def); err != nil {
		return nil, err
	}
	name, err := dictKeyString(key)
	if err != nil {
		return nil, err
	}
	if res, ok := sMap[name]; ok {
		return res, nil
	}
	return def, nil
}

// dictKeyString normalizes a dict lookup key to its string form: str keys
// are used as-is, anything else goes through str(key) so non-string keys
// (int status codes, bools, ...) address the same string bucket they were
// stored under.
func dictKeyString(key py.Object) (string, error) {
	if s, ok := key.(py.String); ok {
		return string(s), nil
	}
	return py.StrAsString(key)
}

const dictGetDoc = `get(key, default=None) -> value

Return the value for key if key is in the dictionary, else default.
dict keys are strings: non-string keys are normalized through str(key)
before lookup, so d.get(200) finds the value stored under '200'.`

func init() {
	registerPyDictMethods()
}
