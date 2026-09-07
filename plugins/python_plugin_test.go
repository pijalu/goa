// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePythonPlugin writes a minimal plugin dir with a plugin.yaml + plugin.py
// entry for loader tests.
func writePythonPlugin(t *testing.T, manifest, src string) string {
	t.Helper()
	dir := t.TempDir()
	plugDir := filepath.Join(dir, "pyplug")
	if err := os.MkdirAll(plugDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugDir, "plugin.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugDir, "plugin.py"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

const pyPluginManifest = "id: pyplug\nname: Py Plug\nversion: 0.1.0\nentry: plugin.py\npermissions: [network]\n"

// TestPythonBridge_RegisterTool pins the Python plugin contract: a plugin.py
// calling goa.register_tool must reach the RegisterTool handler, and the
// captured execute closure must round-trip params.
func TestPythonBridge_RegisterTool(t *testing.T) {
	var capturedName, capturedDesc string
	var capturedExecute func(map[string]any) (interface{}, error)

	ctx := PluginContext{
		Config: map[string]any{},
		Logger: testLogger(),
		RegisterTool: func(name, description string, execute func(map[string]any) (interface{}, error)) error {
			capturedName = name
			capturedDesc = description
			capturedExecute = execute
			return nil
		},
	}

	src := "goa.register_tool({\"name\": \"py_tool\", \"description\": \"A python tool\", \"execute\": lambda params: \"result: \" + str(params)})\n"
	bridge := NewPythonBridge(PluginDef{ID: "pyplug", Name: "Py Plug", Entry: "plugin.py", Permissions: []string{"network"}}, ctx)
	if err := bridge.RunSource(src); err != nil {
		t.Fatalf("RunSource: %v", err)
	}
	if capturedName != "py_tool" {
		t.Fatalf("name = %q, want py_tool", capturedName)
	}
	if capturedDesc != "A python tool" {
		t.Fatalf("description = %q, want %q", capturedDesc, "A python tool")
	}
	if capturedExecute == nil {
		t.Fatal("execute not registered")
	}
	out, err := capturedExecute(map[string]any{"key": "value"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	s, _ := out.(string)
	if !strings.Contains(s, "value") {
		t.Fatalf("execute result = %v, want it to contain 'value'", out)
	}
}

// TestPythonBridge_RegisterCommand pins goa.register_command for Python
// plugins: name/aliases/help parse and the run closure executes.
func TestPythonBridge_RegisterCommand(t *testing.T) {
	var capturedName string
	var capturedAliases []string
	var capturedRun func([]string) (string, error)

	ctx := PluginContext{
		Config: map[string]any{},
		Logger: testLogger(),
		RegisterCommand: func(name string, aliases []string, shortHelp, longHelp string, run func([]string) (string, error)) error {
			capturedName = name
			capturedAliases = aliases
			capturedRun = run
			return nil
		},
	}

	src := "goa.register_command({\"name\": \"hello\", \"aliases\": [\"h\"], \"shortHelp\": \"Hi\", \"longHelp\": \"Long hi\", \"run\": lambda args: \"Hello, \" + \", \".join(args)})\n"
	bridge := NewPythonBridge(PluginDef{ID: "pyplug", Name: "Py Plug", Entry: "plugin.py"}, ctx)
	if err := bridge.RunSource(src); err != nil {
		t.Fatalf("RunSource: %v", err)
	}
	if capturedName != "hello" {
		t.Fatalf("name = %q, want hello", capturedName)
	}
	if len(capturedAliases) != 1 || capturedAliases[0] != "h" {
		t.Fatalf("aliases = %v, want [h]", capturedAliases)
	}
	out, err := capturedRun([]string{"world"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "world") {
		t.Fatalf("run result = %q, want it to contain 'world'", out)
	}
}

// TestPythonBridge_CallTool pins goa.call_tool from Python: name + params
// reach the CallTool handler and the result returns.
func TestPythonBridge_CallTool(t *testing.T) {
	var capturedName string
	var capturedParams map[string]any

	ctx := PluginContext{
		Config: map[string]any{},
		Logger: testLogger(),
		CallTool: func(name string, params map[string]any) (interface{}, error) {
			capturedName = name
			capturedParams = params
			return "tool_result_ok", nil
		},
	}

	src := "__res = goa.call_tool(\"read\", {\"path\": \"x.txt\"})\n"
	bridge := NewPythonBridge(PluginDef{ID: "pyplug", Name: "Py Plug", Entry: "plugin.py"}, ctx)
	if err := bridge.RunSource(src); err != nil {
		t.Fatalf("RunSource: %v", err)
	}
	if capturedName != "read" {
		t.Fatalf("tool name = %q, want read", capturedName)
	}
	if capturedParams["path"] != "x.txt" {
		t.Fatalf("params = %v, want path x.txt", capturedParams)
	}
	if got := bridge.GlobalString("__res"); got != "tool_result_ok" {
		t.Fatalf("__res = %q, want tool_result_ok", got)
	}
}

// TestPythonBridge_HttpFetchGated pins the network gate for Python: without
// the "network" permission the fetch fails closed and never reaches HTTP.
func TestPythonBridge_HttpFetchGated(t *testing.T) {
	reachedHook := false
	restore := setHTTPDo(func(b *HTTPBridge, req HTTPRequest) HTTPResponse {
		reachedHook = true
		return HTTPResponse{Status: 200, Body: "should-not-leak"}
	})
	defer restore()

	ctx := newExtendedContext(t, t.TempDir(), NewHTTPBridge())
	bridge := NewPythonBridge(PluginDef{ID: "pyplug", Name: "Py Plug", Entry: "plugin.py"}, ctx) // no permissions
	if err := bridge.RunSource("__res = str(goa.http.fetch(\"https://example.com/quota\"))\n"); err != nil {
		t.Fatalf("RunSource: %v", err)
	}
	got := bridge.GlobalString("__res")
	if strings.Contains(got, "should-not-leak") {
		t.Fatalf("HTTP body leaked without network permission: %s", got)
	}
	if !strings.Contains(got, "network") || !strings.Contains(got, "permission") {
		t.Fatalf("result = %s, want network permission error", got)
	}
	if reachedHook {
		t.Fatal("HTTP hook reached despite missing network permission")
	}
}

// TestPythonBridge_HttpFetchAllowed pins the open gate: with the permission,
// the fetch reaches the HTTP hook and the body returns.
func TestPythonBridge_HttpFetchAllowed(t *testing.T) {
	restore := setHTTPDo(func(b *HTTPBridge, req HTTPRequest) HTTPResponse {
		return HTTPResponse{Status: 200, Body: `{"plan":"pro"}`}
	})
	defer restore()

	ctx := newExtendedContext(t, t.TempDir(), NewHTTPBridge())
	bridge := NewPythonBridge(PluginDef{ID: "pyplug", Name: "Py Plug", Entry: "plugin.py", Permissions: []string{"network"}}, ctx)
	if err := bridge.RunSource("__res = goa.http.fetch(\"https://example.com/quota\")[\"body\"]\n"); err != nil {
		t.Fatalf("RunSource: %v", err)
	}
	if got := bridge.GlobalString("__res"); !strings.Contains(got, `"plan":"pro"`) {
		t.Fatalf("__res = %q, want fetched body", got)
	}
}

// TestPythonPluginLoader_LoadsPyEntry pins loader dispatch: a plugin dir with
// entry plugin.py loads a Python bridge through the shared PluginLoader.
func TestPythonPluginLoader_LoadsPyEntry(t *testing.T) {
	dir := writePythonPlugin(t, pyPluginManifest,
		"goa.register_command({\"name\": \"pyquota\", \"run\": lambda args: \"py-quota-ok\"})\n")

	var commands []string
	ctx := PluginContext{
		Config: map[string]any{},
		Logger: testLogger(),
		RegisterCommand: func(name string, aliases []string, shortHelp, longHelp string, run func([]string) (string, error)) error {
			commands = append(commands, name)
			return nil
		},
	}
	pl := NewPluginLoader([]string{dir}, []string{"pyplug"})
	bridges, perr := pl.LoadAll(ctx)
	if perr != nil {
		t.Fatalf("LoadAll: %v", perr)
	}
	if len(bridges) != 1 {
		t.Fatalf("expected 1 bridge, got %d", len(bridges))
	}
	if len(commands) != 1 || commands[0] != "pyquota" {
		t.Fatalf("commands = %v, want [pyquota]", commands)
	}
	if bridges[0].Kind() != PluginKindPython {
		t.Fatalf("bridge kind = %q, want python", bridges[0].Kind())
	}
}
