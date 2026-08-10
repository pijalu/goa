// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"sync"

	"github.com/pijalu/gpython/py"
)

// pyFileMethodsOnce guards the one-time registration of methods the embedded
// gpython py.FileType omits. gpython's own init registers read/readline/
// write/close/flush/iteration; this adds the remaining common file-object
// methods (readlines) so idiomatic LLM Python such as `f.readlines()` runs
// unmodified. Registration is idempotent and patches the single global
// py.FileType, so it is available to every interpreter context.
var pyFileMethodsOnce sync.Once

// registerPyFileMethods patches the global py.FileType with the file-object
// methods gpython omits. It is safe to call from any interpreter setup path.
func registerPyFileMethods() {
	pyFileMethodsOnce.Do(func() {
		py.FileType.Dict["readlines"] = py.MustNewMethod("readlines",
			func(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
				return fileReadlines(self, args, kwargs)
			}, 0, fileReadlinesDoc)
	})
}

// fileReadlines implements file.readlines([hint]) on a *py.File: it repeatedly
// calls readline until EOF, collecting each line (terminator included) into a
// list, and stops once the cumulative byte length reaches a non-negative hint.
func fileReadlines(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	f, ok := self.(*py.File)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError,
			"readlines() requires a file object, not %s", self.Type().Name)
	}
	var hint py.Object = py.None
	if err := py.UnpackTuple(args, kwargs, "readlines", 0, 1, &hint); err != nil {
		return nil, err
	}
	limit := int64(-1)
	if hint != py.None {
		pyN, ok := hint.(py.Int)
		if !ok {
			return nil, py.ExceptionNewf(py.TypeError,
				"integer argument expected, got '%s'", hint.Type().Name)
		}
		limit, _ = pyN.GoInt64()
	}
	out := py.NewList()
	var total int64
	for {
		line, err := f.ReadLine(py.Tuple{py.Int(-1)}, nil)
		if err != nil {
			return nil, err
		}
		n := pyLineLen(line)
		if n == 0 { // EOF: ReadLine returns "" / empty Bytes at end of stream.
			break
		}
		out.Append(line)
		total += int64(n)
		if limit >= 0 && total >= limit {
			break
		}
	}
	return out, nil
}

// pyLineLen returns the byte length of a file readline result (String or Bytes);
// any other type is treated as zero-length, which terminates readlines.
func pyLineLen(o py.Object) int {
	switch v := o.(type) {
	case py.String:
		return len(v)
	case py.Bytes:
		return len(v)
	}
	return 0
}

const fileReadlinesDoc = `readlines(hint=-1, /) -> list of str

Read and return a list of lines from the stream. If hint is specified and
non-negative, reading stops once the cumulative byte length of the returned
lines reaches hint (the line being read may exceed hint). The line terminator
is included in each returned line.`

func init() {
	registerPyFileMethods()
}
