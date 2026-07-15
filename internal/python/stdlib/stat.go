// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib

import (
	"github.com/go-python/gpython/py"
)

func init() {
	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "stat",
			Doc:  "stat — constants and helpers for os.stat results",
		},
		Methods: []*py.Method{
			py.MustNewMethod("S_IMODE", statSImode, 0, `S_IMODE(mode) -> int

Return the permission bits of mode.`),
			py.MustNewMethod("S_IFMT", statSIfmt, 0, `S_IFMT(mode) -> int

Return the file type bits of mode.`),
		},
		Globals: py.StringDict{
			"S_IFREG": py.Int(0o100000),
			"S_IFDIR": py.Int(0o040000),
			"S_IRUSR": py.Int(0o400),
			"S_IWUSR": py.Int(0o200),
			"S_IXUSR": py.Int(0o100),
			"S_IRGRP": py.Int(0o040),
			"S_IWGRP": py.Int(0o020),
			"S_IXGRP": py.Int(0o010),
			"S_IROTH": py.Int(0o004),
			"S_IWOTH": py.Int(0o002),
			"S_IXOTH": py.Int(0o001),
		},
	})
}

// S_IMODE returns the permission bits of a mode value.
func statSImode(self py.Object, args py.Tuple) (py.Object, error) {
	var mode py.Object
	if err := py.UnpackTuple(args, nil, "S_IMODE", 1, 1, &mode); err != nil {
		return nil, err
	}
	m, ok := mode.(py.Int)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "S_IMODE() argument must be int, not %s", mode.Type().Name)
	}
	return py.Int(int(m) & 0o7777), nil
}

// S_IFMT returns the file-type bits of a mode value.
func statSIfmt(self py.Object, args py.Tuple) (py.Object, error) {
	var mode py.Object
	if err := py.UnpackTuple(args, nil, "S_IFMT", 1, 1, &mode); err != nil {
		return nil, err
	}
	m, ok := mode.(py.Int)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "S_IFMT() argument must be int, not %s", mode.Type().Name)
	}
	return py.Int(int(m) & 0o170000), nil
}
