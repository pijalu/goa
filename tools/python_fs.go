// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pijalu/gpython/py"
)

// This file implements a curated, jail-aware replacement for the embedded
// gpython `os` module (and an `os.path` submodule). It is installed per
// interpreter context so that `import os` resolves to it, letting typical
// LLM Python file code run unmodified while confined to the project jail.
//
// The stock gpython `os` module is both too sparse (no walk/stat/path) and
// unsafe (exposes os.system / os._exit with no confinement). This replacement
// provides the common file API under standard names and intentionally omits
// shell/exit primitives.

// stat mode bits, matching CPython's stat module.
const (
	s_IFREG = 0o100000
	s_IFDIR = 0o040000
)

// pyFileFunc is the implementation signature shared by every os/os.path
// method: it receives the jail scope plus the call's positional and keyword
// arguments.
type pyFileFunc func(scope pyFileScope, args py.Tuple, kwargs py.StringDict) (py.Object, error)

// pyFileScope resolves interpreter file paths against an optional jail root.
// When confined is true, every resolved path must stay within root.
type pyFileScope struct {
	root     string // absolute, cleaned; empty when no root is configured
	confined bool   // when true, paths escaping root are rejected
}

// newPyFileScope builds a scope from tool configuration. When jail is enabled
// and projectDir resolves to an absolute path, file operations are confined to
// projectDir and below. Otherwise the scope is unconfined and relative paths
// resolve against the host working directory.
func newPyFileScope(projectDir string, jail bool) pyFileScope {
	if projectDir == "" {
		return pyFileScope{}
	}
	abs, err := filepath.Abs(filepath.Clean(projectDir))
	if err != nil {
		return pyFileScope{}
	}
	return pyFileScope{root: abs, confined: jail}
}

// base returns the directory that relative paths resolve against. It doubles
// as os.getcwd() so the interpreter "thinks" it is rooted at the project.
func (s pyFileScope) base() string {
	if s.root != "" {
		return s.root
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

// resolve returns the absolute filesystem path for a user-supplied path,
// rejecting escapes from the jail root when confined.
func (s pyFileScope) resolve(p string) (string, error) {
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Clean(filepath.Join(s.base(), p))
	}
	if s.confined && !pathUnderAbsRoot(abs, s.root) {
		return "", py.ExceptionNewf(py.PermissionError,
			"path %q escapes jail root %q", p, s.root)
	}
	return abs, nil
}

// pathUnderAbsRoot reports whether abs (cleaned, absolute) is equal to or
// nested under root (cleaned, absolute). Lexical comparison only, matching the
// confinement guarantees of the bash jail.
func pathUnderAbsRoot(abs, root string) bool {
	if abs == root {
		return true
	}
	return strings.HasPrefix(abs, root+string(filepath.Separator))
}

// osStatResult is the Go backing for the object returned by os.stat / lstat.
// It exposes the attributes CPython code reads most: st_size, st_mtime,
// st_mode, st_nlink.
type osStatResult struct {
	info os.FileInfo
}

var osStatResultType = py.NewType("os.stat_result", "os.stat_result(...)")

// Type satisfies the py.Object interface.
func (s *osStatResult) Type() *py.Type { return osStatResultType }

// M__getattr__ exposes stat fields as attributes (e.g. st.st_size).
func (s *osStatResult) M__getattr__(name string) (py.Object, error) {
	switch name {
	case "st_size":
		return py.Int(s.info.Size()), nil
	case "st_mtime":
		return py.Float(float64(s.info.ModTime().UnixNano()) / float64(time.Second)), nil
	case "st_mode":
		return py.Int(osStatMode(s.info)), nil
	case "st_nlink":
		return py.Int(1), nil
	}
	return nil, py.ExceptionNewf(py.AttributeError,
		"'stat_result' object has no attribute '%s'", name)
}

// osStatMode returns a CPython-compatible st_mode (permission bits plus the
// IFREG/IFDIR file-type bit).
func osStatMode(info os.FileInfo) uint32 {
	m := uint32(info.Mode().Perm())
	if info.IsDir() {
		m |= s_IFDIR
	} else {
		m |= s_IFREG
	}
	return m
}

// installGoaFsModules installs the jail-aware os and os.path modules into a
// fresh interpreter context so `import os` resolves to the curated, confined
// implementation. It also overrides the built-in open() so the common file
// idiom is jail-confined (the stock open() calls os.OpenFile directly).
func installGoaFsModules(ctx py.Context, scope pyFileScope) error {
	pathMod, err := ctx.Store().NewModule(ctx, buildOSPathModule(scope))
	if err != nil {
		return err
	}
	osMod, err := ctx.Store().NewModule(ctx, buildOSModule(scope))
	if err != nil {
		return err
	}
	// Wire os.path so both `os.path.join(...)` and `import os.path` resolve.
	osMod.Globals["path"] = pathMod
	// Override the built-in open with a jail-aware version.
	builtins, err := ctx.GetModule("builtins")
	if err != nil {
		return err
	}
	builtins.Globals["open"] = gMethod("open", osBuiltinOpenDoc, osBuiltinOpen, scope)
	return nil
}

// gMethod binds a pyFileFunc into a gpython module Method whose closure
// captures the jail scope.
func gMethod(name, doc string, fn pyFileFunc, scope pyFileScope) *py.Method {
	return py.MustNewMethod(name, func(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
		return fn(scope, args, kwargs)
	}, 0, doc)
}

// reqStr extracts the single required string positional argument at index 0.
func reqStr(args py.Tuple, fn string) (string, error) {
	if len(args) < 1 {
		return "", py.ExceptionNewf(py.TypeError, "%s() requires a path argument", fn)
	}
	return asString(args[0], fn)
}

// optStr extracts an optional string positional argument, returning def when
// absent or None.
func optStr(args py.Tuple, idx int, def, fn string) (string, error) {
	if idx >= len(args) || args[idx] == py.None {
		return def, nil
	}
	return asString(args[idx], fn)
}

// kwStr returns a string kwarg or def when absent/None/non-str.
func kwStr(kwargs py.StringDict, key, def string) string {
	if kwargs == nil {
		return def
	}
	v, ok := kwargs[key]
	if !ok || v == py.None {
		return def
	}
	if s, ok := v.(py.String); ok {
		return string(s)
	}
	return def
}

// kwBool returns a bool kwarg or def when absent/None/non-bool.
func kwBool(kwargs py.StringDict, key string, def bool) bool {
	if kwargs == nil {
		return def
	}
	v, ok := kwargs[key]
	if !ok || v == py.None {
		return def
	}
	b, ok := v.(py.Bool)
	if !ok {
		return def
	}
	return bool(b)
}

// asString coerces a py.Object to a Go string for path arguments.
func asString(o py.Object, fn string) (string, error) {
	switch v := o.(type) {
	case py.String:
		return string(v), nil
	case py.Bytes:
		return string(v), nil
	}
	return "", py.ExceptionNewf(py.TypeError, "%s() argument must be str, not %s", fn, o.Type().Name)
}

// pyOSError translates a Go filesystem error into the closest Python type.
func pyOSError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case os.IsNotExist(err):
		return py.ExceptionNewf(py.FileNotFoundError, "%v", err)
	case os.IsPermission(err):
		return py.ExceptionNewf(py.PermissionError, "%v", err)
	case os.IsExist(err):
		return py.ExceptionNewf(py.FileExistsError, "%v", err)
	}
	return py.ExceptionNewf(py.OSError, "%v", err)
}

// osSepValue returns the platform path separator as a py.String.
func osSepValue() py.String { return py.String(string(os.PathSeparator)) }

// osNameValue returns the CPython-style os.name for the host platform.
func osNameValue() string {
	if os.PathSeparator == '\\' {
		return "nt"
	}
	return "posix"
}

// osEnvironDict returns a snapshot of the process environment as a dict.
func osEnvironDict() py.StringDict {
	d := py.NewStringDict()
	for _, e := range os.Environ() {
		if k, v, ok := strings.Cut(e, "="); ok {
			d[k] = py.String(v)
		}
	}
	return d
}

// buildOSModule constructs the jail-aware os module for a context.
func buildOSModule(scope pyFileScope) *py.ModuleImpl {
	return &py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "os",
			Doc:  "os — curated, jail-confined file interface for the embedded interpreter.",
		},
		Methods: []*py.Method{
			gMethod("getcwd", osGetcwdDoc, osGetcwd, scope),
			gMethod("chdir", osChdirDoc, osChdir, scope),
			gMethod("listdir", osListdirDoc, osListdir, scope),
			gMethod("walk", osWalkDoc, osWalk, scope),
			gMethod("stat", osStatDoc, osStat, scope),
			gMethod("lstat", osStatDoc, osStat, scope),
			gMethod("mkdir", osMkdirDoc, osMkdir, scope),
			gMethod("makedirs", osMakedirsDoc, osMakedirs, scope),
			gMethod("remove", osRemoveDoc, osRemove, scope),
			gMethod("unlink", osRemoveDoc, osRemove, scope),
			gMethod("rmdir", osRmdirDoc, osRmdir, scope),
			gMethod("removedirs", osRemovedirsDoc, osRemovedirs, scope),
			gMethod("getenv", osGetenvDoc, osGetenv, scope),
			gMethod("getpid", osGetpidDoc, osGetpid, scope),
		},
		Globals: osGlobals(scope),
	}
}

// osGlobals returns the non-callable module globals (constants + environ).
func osGlobals(scope pyFileScope) py.StringDict {
	return py.StringDict{
		"sep":     osSepValue(),
		"altsep":  py.None,
		"linesep": py.String("\n"),
		"name":    py.String(osNameValue()),
		"curdir":  py.String("."),
		"pardir":  py.String(".."),
		"extsep":  py.String("."),
		"pathsep": py.String(string(os.PathListSeparator)),
		"defpath": py.String(":/bin:/usr/bin"),
		"devnull": py.String("/dev/null"),
		"error":   py.OSError,
		"environ": osEnvironDict(),
		"_root":   py.String(scope.root),
	}
}

const osGetcwdDoc = `getcwd() -> str

Return the current working directory (the jail root).`

func osGetcwd(scope pyFileScope, _ py.Tuple, _ py.StringDict) (py.Object, error) {
	return py.String(scope.base()), nil
}

const osChdirDoc = `chdir(path) -> None

Validate path against the jail. The interpreter always resolves relative
paths against the jail root, so chdir is effectively a no-op within the jail.`

func osChdir(scope pyFileScope, args py.Tuple, _ py.StringDict) (py.Object, error) {
	p, err := reqStr(args, "chdir")
	if err != nil {
		return nil, err
	}
	if _, err := scope.resolve(p); err != nil {
		return nil, err
	}
	return py.None, nil
}

const osListdirDoc = `listdir(path=".") -> list of str

Return the names of entries in path, excluding "." and "..".`

func osListdir(scope pyFileScope, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	p, err := optStr(args, 0, ".", "listdir")
	if err != nil {
		return nil, err
	}
	abs, err := scope.resolve(kwStr(kwargs, "path", p))
	if err != nil {
		return nil, err
	}
	entries, rerr := os.ReadDir(abs)
	if rerr != nil {
		return nil, pyOSError(rerr)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return py.NewListFromStrings(names), nil
}

const osWalkDoc = `walk(top) -> list of (dirpath, dirnames, filenames)

Yield, top-down, one tuple per directory beneath top. dirnames and filenames
are the direct children of dirpath.`

func osWalk(scope pyFileScope, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	p, err := optStr(args, 0, ".", "walk")
	if err != nil {
		return nil, err
	}
	abs, err := scope.resolve(kwStr(kwargs, "top", p))
	if err != nil {
		return nil, err
	}
	return osWalkCollect(scope, abs)
}

// osDirChildren holds the direct child dir/file names for one directory
// during an os.walk traversal.
type osDirChildren struct{ dirs, files []string }

// osWalker accumulates (dirpath, dirnames, filenames) groups in top-down order.
type osWalker struct {
	scope    pyFileScope
	root     string
	children map[string]*osDirChildren
	order    []string
}

func newOSWalker(scope pyFileScope, root string) *osWalker {
	w := &osWalker{
		scope:    scope,
		root:     root,
		children: make(map[string]*osDirChildren),
	}
	w.ensure(root)
	return w
}

// ensure returns the children entry for d, creating and ordering it if needed.
func (w *osWalker) ensure(d string) *osDirChildren {
	if c, ok := w.children[d]; ok {
		return c
	}
	c := &osDirChildren{}
	w.children[d] = c
	w.order = append(w.order, d)
	return c
}

// walkFn is the filepath.WalkDir callback that bins each entry under its parent.
func (w *osWalker) walkFn(path string, d os.DirEntry, err error) error {
	if err != nil {
		return err
	}
	if path == w.root {
		return nil
	}
	if w.scope.confined && !pathUnderAbsRoot(path, w.scope.root) {
		if d != nil && d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	}
	parent := w.ensure(filepath.Dir(path))
	if d.IsDir() {
		w.ensure(path)
		parent.dirs = append(parent.dirs, d.Name())
	} else {
		parent.files = append(parent.files, d.Name())
	}
	return nil
}

// tuples builds the list of (dirpath, dirnames, filenames) result tuples.
func (w *osWalker) tuples() *py.List {
	out := py.NewList()
	for _, dir := range w.order {
		c := w.children[dir]
		out.Append(py.Tuple{
			py.String(dir),
			py.NewListFromStrings(c.dirs),
			py.NewListFromStrings(c.files),
		})
	}
	return out
}

// osWalkCollect performs the directory walk and returns the os.walk tuples.
func osWalkCollect(scope pyFileScope, abs string) (py.Object, error) {
	w := newOSWalker(scope, abs)
	if err := filepath.WalkDir(abs, w.walkFn); err != nil {
		return nil, pyOSError(err)
	}
	return w.tuples(), nil
}

const osStatDoc = `stat(path) -> stat_result

Return a stat_result with attributes st_size, st_mtime, st_mode, st_nlink.`

func osStat(scope pyFileScope, args py.Tuple, _ py.StringDict) (py.Object, error) {
	p, err := reqStr(args, "stat")
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
	return &osStatResult{info: info}, nil
}

const osMkdirDoc = `mkdir(path) -> None

Create a leaf directory.`

func osMkdir(scope pyFileScope, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	p := kwStr(kwargs, "path", "")
	if p == "" {
		var err error
		if p, err = optStr(args, 0, "", "mkdir"); err != nil {
			return nil, err
		}
	}
	if p == "" {
		return nil, py.ExceptionNewf(py.TypeError, "mkdir() requires a path argument")
	}
	abs, err := scope.resolve(p)
	if err != nil {
		return nil, err
	}
	if merr := os.Mkdir(abs, 0o755); merr != nil {
		return nil, pyOSError(merr)
	}
	return py.None, nil
}

const osMakedirsDoc = `makedirs(path, exist_ok=False) -> None

Create a directory and any necessary parents.`

func osMakedirs(scope pyFileScope, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	p := kwStr(kwargs, "path", "")
	if p == "" {
		var err error
		if p, err = optStr(args, 0, "", "makedirs"); err != nil {
			return nil, err
		}
	}
	if p == "" {
		return nil, py.ExceptionNewf(py.TypeError, "makedirs() requires a path argument")
	}
	abs, err := scope.resolve(p)
	if err != nil {
		return nil, err
	}
	if !kwBool(kwargs, "exist_ok", false) {
		if _, serr := os.Stat(abs); serr == nil {
			return nil, py.ExceptionNewf(py.FileExistsError, "File exists: '%s'", abs)
		}
	}
	if merr := os.MkdirAll(abs, 0o755); merr != nil {
		return nil, pyOSError(merr)
	}
	return py.None, nil
}

const osRemoveDoc = `remove(path) -> None

Remove a file.`

func osRemove(scope pyFileScope, args py.Tuple, _ py.StringDict) (py.Object, error) {
	p, err := reqStr(args, "remove")
	if err != nil {
		return nil, err
	}
	abs, err := scope.resolve(p)
	if err != nil {
		return nil, err
	}
	if rerr := os.Remove(abs); rerr != nil {
		return nil, pyOSError(rerr)
	}
	return py.None, nil
}

const osRmdirDoc = `rmdir(path) -> None

Remove an empty directory.`

func osRmdir(scope pyFileScope, args py.Tuple, _ py.StringDict) (py.Object, error) {
	p, err := reqStr(args, "rmdir")
	if err != nil {
		return nil, err
	}
	abs, err := scope.resolve(p)
	if err != nil {
		return nil, err
	}
	if rerr := os.Remove(abs); rerr != nil {
		return nil, pyOSError(rerr)
	}
	return py.None, nil
}

const osRemovedirsDoc = `removedirs(path) -> None

Remove path and any now-empty parent directories.`

func osRemovedirs(scope pyFileScope, args py.Tuple, _ py.StringDict) (py.Object, error) {
	p, err := reqStr(args, "removedirs")
	if err != nil {
		return nil, err
	}
	abs, err := scope.resolve(p)
	if err != nil {
		return nil, err
	}
	if rerr := os.RemoveAll(abs); rerr != nil {
		return nil, pyOSError(rerr)
	}
	return py.None, nil
}

const osGetenvDoc = `getenv(name, default=None) -> str

Return the value of environment variable name, or default if unset.`

func osGetenv(_ pyFileScope, args py.Tuple, _ py.StringDict) (py.Object, error) {
	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "getenv() requires a name argument")
	}
	name, err := asString(args[0], "getenv")
	if err != nil {
		return nil, err
	}
	if v, ok := os.LookupEnv(name); ok {
		return py.String(v), nil
	}
	if len(args) >= 2 {
		return args[1], nil
	}
	return py.None, nil
}

const osGetpidDoc = `getpid() -> int

Return the host process id.`

func osGetpid(_ pyFileScope, _ py.Tuple, _ py.StringDict) (py.Object, error) {
	return py.Int(os.Getpid()), nil
}

const osBuiltinOpenDoc = `open(file, mode='r') -> file

Open a file, jail-confined to the project root. Supports the common modes
('r', 'w', 'a', optionally with 'b' or '+'). Returns a file object supporting
read(), write(), close(), and the with-statement.`

func osBuiltinOpen(scope pyFileScope, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	var filename py.Object
	var mode py.Object = py.String("r")
	if err := py.ParseTupleAndKeywords(args, kwargs, "s|s:open",
		[]string{"file", "mode"}, &filename, &mode); err != nil {
		return nil, err
	}
	abs, err := scope.resolve(string(filename.(py.String)))
	if err != nil {
		return nil, err
	}
	return py.OpenFile(abs, string(mode.(py.String)), -1)
}
