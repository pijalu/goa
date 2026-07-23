// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"os"
	"path/filepath"

	"github.com/pijalu/gpython/py"
)

// This file implements the jail-aware os.path submodule that accompanies the
// curated os module in python_fs.go.

// buildOSPathModule constructs the jail-aware os.path submodule.
func buildOSPathModule(scope pyFileScope) *py.ModuleImpl {
	return &py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "os.path",
			Doc:  "os.path — curated path operations for the embedded interpreter.",
		},
		Methods: []*py.Method{
			gMethod("join", pathJoinDoc, pathJoin, scope),
			gMethod("split", pathSplitDoc, pathSplit, scope),
			gMethod("splitext", pathSplitextDoc, pathSplitext, scope),
			gMethod("basename", pathBasenameDoc, pathBasename, scope),
			gMethod("dirname", pathDirnameDoc, pathDirname, scope),
			gMethod("abspath", pathAbspathDoc, pathAbspath, scope),
			gMethod("normpath", pathNormpathDoc, pathNormpath, scope),
			gMethod("relpath", pathRelpathDoc, pathRelpath, scope),
			gMethod("isabs", pathIsabsDoc, pathIsabs, scope),
			gMethod("exists", pathExistsDoc, pathExists, scope),
			gMethod("isfile", pathIsfileDoc, pathIsfile, scope),
			gMethod("isdir", pathIsdirDoc, pathIsdir, scope),
			gMethod("getsize", pathGetsizeDoc, pathGetsize, scope),
		},
		Globals: py.StringDict{
			"sep":     osSepValue(),
			"altsep":  py.None,
			"linesep": py.String("\n"),
			"pathsep": py.String(string(os.PathListSeparator)),
		},
	}
}

const pathJoinDoc = `join(*paths) -> str

Join path components with the platform separator.`

func pathJoin(_ pyFileScope, args py.Tuple, _ py.StringDict) (py.Object, error) {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		s, err := asString(a, "join")
		if err != nil {
			return nil, err
		}
		parts = append(parts, s)
	}
	return py.String(filepath.Join(parts...)), nil
}

const pathSplitDoc = `split(path) -> (head, tail)`

func pathSplit(_ pyFileScope, args py.Tuple, _ py.StringDict) (py.Object, error) {
	p, err := reqStr(args, "split")
	if err != nil {
		return nil, err
	}
	head, tail := filepath.Split(p)
	return py.Tuple{py.String(head), py.String(tail)}, nil
}

const pathSplitextDoc = `splitext(path) -> (root, ext)`

func pathSplitext(_ pyFileScope, args py.Tuple, _ py.StringDict) (py.Object, error) {
	p, err := reqStr(args, "splitext")
	if err != nil {
		return nil, err
	}
	ext := filepath.Ext(p)
	return py.Tuple{py.String(p[:len(p)-len(ext)]), py.String(ext)}, nil
}

const pathBasenameDoc = `basename(path) -> str`

func pathBasename(_ pyFileScope, args py.Tuple, _ py.StringDict) (py.Object, error) {
	p, err := reqStr(args, "basename")
	if err != nil {
		return nil, err
	}
	return py.String(filepath.Base(p)), nil
}

const pathDirnameDoc = `dirname(path) -> str`

func pathDirname(_ pyFileScope, args py.Tuple, _ py.StringDict) (py.Object, error) {
	p, err := reqStr(args, "dirname")
	if err != nil {
		return nil, err
	}
	return py.String(filepath.Dir(p)), nil
}

const pathAbspathDoc = `abspath(path) -> str

Return the absolute path, resolved against the jail root.`

func pathAbspath(scope pyFileScope, args py.Tuple, _ py.StringDict) (py.Object, error) {
	p, err := reqStr(args, "abspath")
	if err != nil {
		return nil, err
	}
	abs, err := scope.resolve(p)
	if err != nil {
		return nil, err
	}
	return py.String(abs), nil
}

const pathNormpathDoc = `normpath(path) -> str`

func pathNormpath(_ pyFileScope, args py.Tuple, _ py.StringDict) (py.Object, error) {
	p, err := reqStr(args, "normpath")
	if err != nil {
		return nil, err
	}
	return py.String(filepath.Clean(p)), nil
}

const pathRelpathDoc = `relpath(path, start=root) -> str`

func pathRelpath(scope pyFileScope, args py.Tuple, _ py.StringDict) (py.Object, error) {
	p, err := reqStr(args, "relpath")
	if err != nil {
		return nil, err
	}
	start, err := optStr(args, 1, scope.base(), "relpath")
	if err != nil {
		return nil, err
	}
	abs, err := scope.resolve(p)
	if err != nil {
		return nil, err
	}
	startAbs, err := scope.resolve(start)
	if err != nil {
		return nil, err
	}
	rel, rerr := filepath.Rel(startAbs, abs)
	if rerr != nil {
		return nil, pyOSError(rerr)
	}
	return py.String(rel), nil
}

const pathIsabsDoc = `isabs(path) -> bool`

func pathIsabs(_ pyFileScope, args py.Tuple, _ py.StringDict) (py.Object, error) {
	p, err := reqStr(args, "isabs")
	if err != nil {
		return nil, err
	}
	return py.Bool(filepath.IsAbs(p)), nil
}

const pathExistsDoc = `exists(path) -> bool`

func pathExists(scope pyFileScope, args py.Tuple, _ py.StringDict) (py.Object, error) {
	p, err := reqStr(args, "exists")
	if err != nil {
		return nil, err
	}
	abs, err := scope.resolve(p)
	if err != nil {
		return nil, err
	}
	_, serr := os.Stat(abs)
	return py.Bool(serr == nil), nil
}

const pathIsfileDoc = `isfile(path) -> bool`

func pathIsfile(scope pyFileScope, args py.Tuple, _ py.StringDict) (py.Object, error) {
	p, err := reqStr(args, "isfile")
	if err != nil {
		return nil, err
	}
	abs, err := scope.resolve(p)
	if err != nil {
		return nil, err
	}
	info, serr := os.Stat(abs)
	if serr != nil {
		return py.False, nil
	}
	return py.Bool(!info.IsDir()), nil
}

const pathIsdirDoc = `isdir(path) -> bool`

func pathIsdir(scope pyFileScope, args py.Tuple, _ py.StringDict) (py.Object, error) {
	p, err := reqStr(args, "isdir")
	if err != nil {
		return nil, err
	}
	abs, err := scope.resolve(p)
	if err != nil {
		return nil, err
	}
	info, serr := os.Stat(abs)
	if serr != nil {
		return py.False, nil
	}
	return py.Bool(info.IsDir()), nil
}

const pathGetsizeDoc = `getsize(path) -> int`

func pathGetsize(scope pyFileScope, args py.Tuple, _ py.StringDict) (py.Object, error) {
	p, err := reqStr(args, "getsize")
	if err != nil {
		return nil, err
	}
	abs, err := scope.resolve(p)
	if err != nil {
		return nil, err
	}
	info, serr := os.Stat(abs)
	if serr != nil {
		return nil, pyOSError(serr)
	}
	return py.Int(info.Size()), nil
}
