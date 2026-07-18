// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

// writePluginFile writes a file under dir, creating parents.
func writePluginFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func loadPluginFromDisk(t *testing.T, dir string) *JSBridge {
	t.Helper()
	noop := func(string) {}
	ctx := PluginContext{
		Config: map[string]any{},
		Logger: LoggerAPI{Info: noop, Warn: noop, Error: noop, Debug: noop},
	}
	bridge, err := LoadFrom(dir, ctx)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	return bridge
}

func TestRequire_LoadsModule(t *testing.T) {
	dir := t.TempDir()
	writePluginFile(t, dir, "plugin.yaml", "id: test\nname: T\nentry: plugin.js\n")
	writePluginFile(t, dir, "plugin.js", `
		var fmt = require("./lib/format.js");
		__result = fmt.pct(42);
	`)
	writePluginFile(t, dir, "lib/format.js", `
		exports.pct = function(n) { return n + "%"; };
	`)
	bridge := loadPluginFromDisk(t, dir)
	unlock := lockVM()
	defer unlock()
	if got := bridge.vm.Get("__result").String(); got != "42%" {
		t.Fatalf("__result = %q", got)
	}
}

func TestRequire_ModuleExportsReplacement(t *testing.T) {
	dir := t.TempDir()
	writePluginFile(t, dir, "plugin.yaml", "id: t\nname: T\nentry: plugin.js\n")
	writePluginFile(t, dir, "plugin.js", `
		var m = require("./mod.js");
		__result = m.name + ":" + m.value;
	`)
	writePluginFile(t, dir, "mod.js", `
		module.exports = { name: "quota", value: 7 };
	`)
	bridge := loadPluginFromDisk(t, dir)
	unlock := lockVM()
	defer unlock()
	if got := bridge.vm.Get("__result").String(); got != "quota:7" {
		t.Fatalf("__result = %q", got)
	}
}

func TestRequire_CacheReturnsSameObject(t *testing.T) {
	dir := t.TempDir()
	writePluginFile(t, dir, "plugin.yaml", "id: t\nname: T\nentry: plugin.js\n")
	writePluginFile(t, dir, "plugin.js", `
		var a = require("./counter.js");
		var b = require("./counter.js");
		a.increment();
		__result = b.count;
	`)
	writePluginFile(t, dir, "counter.js", `
		exports.count = 0;
		exports.increment = function() { exports.count++; };
	`)
	bridge := loadPluginFromDisk(t, dir)
	unlock := lockVM()
	defer unlock()
	if got := bridge.vm.Get("__result").ToInteger(); got != 1 {
		t.Fatalf("shared module state = %d, want 1 (cache)", got)
	}
}

func TestRequire_NestedRequire(t *testing.T) {
	dir := t.TempDir()
	writePluginFile(t, dir, "plugin.yaml", "id: t\nname: T\nentry: plugin.js\n")
	writePluginFile(t, dir, "plugin.js", `
		var top = require("./lib/top.js");
		__result = top.compute();
	`)
	writePluginFile(t, dir, "lib/top.js", `
		var helper = require("./helper.js");
		exports.compute = function() { return helper.base() * 2; };
	`)
	writePluginFile(t, dir, "lib/helper.js", `
		exports.base = function() { return 21; };
	`)
	bridge := loadPluginFromDisk(t, dir)
	unlock := lockVM()
	defer unlock()
	if got := bridge.vm.Get("__result").ToInteger(); got != 42 {
		t.Fatalf("nested require = %d, want 42", got)
	}
}

func TestRequire_PathTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	writePluginFile(t, dir, "plugin.yaml", "id: t\nname: T\nentry: plugin.js\n")
	writePluginFile(t, dir, "plugin.js", `
		try {
			require("../../etc/passwd");
			__result = "NO-THROW";
		} catch (e) {
			__result = "blocked";
		}
	`)
	bridge := loadPluginFromDisk(t, dir)
	unlock := lockVM()
	defer unlock()
	if got := bridge.vm.Get("__result").String(); got != "blocked" {
		t.Fatalf("traversal not blocked: %q", got)
	}
}

func TestRequire_MissingModuleThrows(t *testing.T) {
	dir := t.TempDir()
	writePluginFile(t, dir, "plugin.yaml", "id: t\nname: T\nentry: plugin.js\n")
	writePluginFile(t, dir, "plugin.js", `
		try { require("./does-not-exist.js"); __result = "NO-THROW"; }
		catch (e) { __result = "threw"; }
	`)
	bridge := loadPluginFromDisk(t, dir)
	unlock := lockVM()
	defer unlock()
	if got := bridge.vm.Get("__result").String(); got != "threw" {
		t.Fatalf("missing module did not throw: %q", got)
	}
}

func TestResolveModulePath(t *testing.T) {
	base := t.TempDir()
	cases := []struct {
		rel     string
		wantErr bool
	}{
		{"./lib/a.js", false},
		{"lib/a", false},       // extension appended
		{"./a", false},         // resolves to a.js
		{"../escape.js", true}, // traversal
		{"/abs/path.js", false}, // absolute joins under base; still confined by hasPathPrefix? joined→base/abs/path.js
		{"", true},              // empty
	}
	for _, c := range cases {
		got, err := resolveModulePath(base, c.rel, base)
		if (err != nil) != c.wantErr {
			t.Errorf("resolveModulePath(%q) err = %v, wantErr %v", c.rel, err, c.wantErr)
		}
		if err == nil && !hasPathPrefix(got, base) && got != base {
			t.Errorf("resolveModulePath(%q) = %q escapes base %q", c.rel, got, base)
		}
	}
}
