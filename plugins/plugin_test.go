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

func TestValidateManifest_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	content := []byte("id: my-plugin\nname: My Plugin\nversion: 0.1.0")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateManifest(path); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateManifest_MissingID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	content := []byte("name: No ID")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateManifest(path); err == nil {
		t.Error("expected error for missing id")
	}
}

func TestValidateManifest_MissingName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	content := []byte("id: test-plugin")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateManifest(path); err == nil {
		t.Error("expected error for missing name")
	}
}

func TestValidateManifest_NonExistentFile(t *testing.T) {
	if err := ValidateManifest("/nonexistent/path.yaml"); err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestLoadManifest_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	content := []byte("id: test\nname: Test Plugin")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	def, err := loadManifest(path)
	if err != nil {
		t.Fatalf("loadManifest: %v", err)
	}
	if def.ID != "test" {
		t.Errorf("expected ID 'test', got %q", def.ID)
	}
	if def.Entry != "plugin.js" {
		t.Errorf("expected default entry 'plugin.js', got %q", def.Entry)
	}
}

func TestLoadManifest_WithCustomEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	content := []byte("id: test\nname: Test Plugin\nentry: custom.js")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	def, err := loadManifest(path)
	if err != nil {
		t.Fatalf("loadManifest: %v", err)
	}
	if def.Entry != "custom.js" {
		t.Errorf("expected entry 'custom.js', got %q", def.Entry)
	}
}

func TestIsEnabled(t *testing.T) {
	if !isEnabled("foo", []string{"foo", "bar"}) {
		t.Error("expected 'foo' to be enabled")
	}
	if isEnabled("baz", []string{"foo", "bar"}) {
		t.Error("expected 'baz' to NOT be enabled")
	}
	if !isEnabled("test", []string{"test"}) {
		t.Error("expected 'test' to be enabled")
	}
}

func TestNewPluginLoader(t *testing.T) {
	pl := NewPluginLoader([]string{"/dir1", "/dir2"}, []string{"*"})
	if pl == nil {
		t.Fatal("NewPluginLoader returned nil")
	}
	if len(pl.dirs) != 2 {
		t.Errorf("expected 2 dirs, got %d", len(pl.dirs))
	}
}

func TestAllEnabled(t *testing.T) {
	pl := NewPluginLoader(nil, []string{"*"})
	if !pl.allEnabled() {
		t.Error("expected allEnabled with [\"*\"]")
	}
	pl2 := NewPluginLoader(nil, []string{"specific"})
	if pl2.allEnabled() {
		t.Error("expected not allEnabled with specific ID")
	}
}

// TestNewJSBridge_RegisterTool verifies that the registerTool bridge function
// correctly parses a JS tool object and calls the RegisterTool handler.
func TestNewJSBridge_RegisterTool(t *testing.T) {
	var capturedName, capturedDesc string
	var capturedExecute func(map[string]interface{}) (interface{}, error)

	ctx := PluginContext{
		Config: map[string]interface{}{},
		Logger: LoggerAPI{
			Info:  func(msg string) {},
			Warn:  func(msg string) {},
			Error: func(msg string) {},
			Debug: func(msg string) {},
		},
		RegisterTool: func(name, description string, execute func(map[string]interface{}) (interface{}, error)) error {
			capturedName = name
			capturedDesc = description
			capturedExecute = execute
			return nil
		},
	}

	def := PluginDef{ID: "test", Name: "Test", Entry: "plugin.js"}
	bridge := NewJSBridge(def, ctx)

	jsCode := `goa.registerTool({
		name: "my_tool",
		description: "A test tool",
		execute: function(params) { return "result: " + JSON.stringify(params); }
	});`

	_, err := bridge.vm.RunString(jsCode)
	if err != nil {
		t.Fatalf("JS execution error: %v", err)
	}

	if capturedName != "my_tool" {
		t.Errorf("expected name 'my_tool', got %q", capturedName)
	}
	if capturedDesc != "A test tool" {
		t.Errorf("expected description 'A test tool', got %q", capturedDesc)
	}
	if capturedExecute == nil {
		t.Fatal("expected execute function to be registered")
	}

	result, err := capturedExecute(map[string]interface{}{"key": "value"})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	resultStr, ok := result.(string)
	if !ok || !strings.Contains(resultStr, "value") {
		t.Errorf("expected execute result to contain 'value', got %v", result)
	}
}

func TestNewJSBridge_RegisterCommand(t *testing.T) {
	var capturedName string
	var capturedAliases []string
	var capturedRun func([]string) (string, error)

	ctx := PluginContext{
		Config: map[string]interface{}{},
		Logger: LoggerAPI{
			Info:  func(msg string) {},
			Warn:  func(msg string) {},
			Error: func(msg string) {},
			Debug: func(msg string) {},
		},
		RegisterCommand: func(name string, aliases []string, shortHelp, longHelp string, run func([]string) (string, error)) error {
			capturedName = name
			capturedAliases = aliases
			capturedRun = run
			return nil
		},
	}

	def := PluginDef{ID: "test", Name: "Test", Entry: "plugin.js"}
	bridge := NewJSBridge(def, ctx)

	jsCode := `goa.registerCommand({
		name: "greet",
		aliases: ["g"],
		shortHelp: "Greet someone",
		longHelp: "Long help for greet",
		run: function(args) { return "Hello, " + args.join(', '); }
	});`

	_, err := bridge.vm.RunString(jsCode)
	if err != nil {
		t.Fatalf("JS execution error: %v", err)
	}

	if capturedName != "greet" {
		t.Errorf("expected name 'greet', got %q", capturedName)
	}
	if len(capturedAliases) != 1 || capturedAliases[0] != "g" {
		t.Errorf("expected aliases [\"g\"], got %v", capturedAliases)
	}

	result, err := capturedRun([]string{"world", "foo"})
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if !strings.Contains(result, "world") {
		t.Errorf("expected result to contain 'world', got %q", result)
	}
}

func TestNewJSBridge_RegisterObserver(t *testing.T) {
	var capturedCallback func(string, interface{})

	ctx := PluginContext{
		Config: map[string]interface{}{},
		Logger: LoggerAPI{
			Info:  func(msg string) {},
			Warn:  func(msg string) {},
			Error: func(msg string) {},
			Debug: func(msg string) {},
		},
		RegisterObserver: func(callback func(string, interface{})) {
			capturedCallback = callback
		},
	}

	def := PluginDef{ID: "test", Name: "Test", Entry: "plugin.js"}
	bridge := NewJSBridge(def, ctx)

	jsCode := `goa.registerObserver(function(event, payload) {
		// observer registered
	});`

	_, err := bridge.vm.RunString(jsCode)
	if err != nil {
		t.Fatalf("JS execution error: %v", err)
	}

	if capturedCallback == nil {
		t.Fatal("expected callback to be registered")
	}

	// Call the captured callback — should not panic
	capturedCallback("test.event", "payload")
}

func TestNewJSBridge_NoHandlerConfigured(t *testing.T) {
	ctx := PluginContext{
		Config: map[string]interface{}{},
		Logger: LoggerAPI{
			Info:  func(msg string) {},
			Warn:  func(msg string) {},
			Error: func(msg string) {},
			Debug: func(msg string) {},
		},
		// No handlers configured
	}

	def := PluginDef{ID: "test", Name: "Test", Entry: "plugin.js"}
	bridge := NewJSBridge(def, ctx)

	jsCode := `goa.registerTool({name: "x", description: "x", execute: function(p) { return "ok"; }});`
	result, err := bridge.vm.RunString(jsCode)
	if err != nil {
		t.Fatalf("JS execution error: %v", err)
	}
	// Should not panic — returns error message string
	if result.String() != "error: ToolHandler not configured" {
		t.Errorf("expected error message about unconfigured handler, got %q", result.String())
	}
}

func TestIsEnabled_EmptyList(t *testing.T) {
	if isEnabled("any", []string{}) {
		t.Error("expected false for empty enabled list")
	}
}

func TestNewJSBridge_CallTool(t *testing.T) {
	var capturedName string
	var capturedParams map[string]interface{}

	ctx := PluginContext{
		Config: map[string]interface{}{},
		Logger: LoggerAPI{
			Info:  func(msg string) {},
			Warn:  func(msg string) {},
			Error: func(msg string) {},
			Debug: func(msg string) {},
		},
		CallTool: func(name string, params map[string]interface{}) (interface{}, error) {
			capturedName = name
			capturedParams = params
			return "tool_result_ok", nil
		},
	}

	def := PluginDef{ID: "test", Name: "Test", Entry: "plugin.js"}
	bridge := NewJSBridge(def, ctx)

	jsCode := `goa.callTool("request_review", {content: "test code"});`
	result, err := bridge.vm.RunString(jsCode)
	if err != nil {
		t.Fatalf("JS execution error: %v", err)
	}

	if capturedName != "request_review" {
		t.Errorf("expected tool name 'request_review', got %q", capturedName)
	}
	if capturedParams["content"] != "test code" {
		t.Errorf("expected params content 'test code', got %v", capturedParams["content"])
	}
	if result.String() != "tool_result_ok" {
		t.Errorf("expected result 'tool_result_ok', got %q", result.String())
	}
}

func TestGoaWizard_PluginYaml_Validates(t *testing.T) {
	// Tests run from the package directory, so resolve relative to the project root
	if err := ValidateManifest("../examples/plugins/goa-wizard/plugin.yaml"); err != nil {
		t.Fatalf("goa-wizard plugin.yaml validation failed: %v", err)
	}
}

func TestGoaWizard_PluginJS_LoadsWithoutCrash(t *testing.T) {
	ctx := PluginContext{
		Config: map[string]interface{}{},
		Logger: LoggerAPI{
			Info:  func(msg string) {},
			Warn:  func(msg string) {},
			Error: func(msg string) {},
			Debug: func(msg string) {},
		},
	}

	// Load the wizard plugin using LoadFrom
	bridge, err := LoadFrom("../examples/plugins/goa-wizard", ctx)
	if err != nil {
		t.Fatalf("LoadFrom goa-wizard failed: %v", err)
	}
	if bridge == nil {
		t.Fatal("LoadFrom returned nil bridge")
	}
}

func TestNewJSBridge_CallTool_NoHandler(t *testing.T) {
	ctx := PluginContext{
		Config: map[string]interface{}{},
		Logger: LoggerAPI{
			Info:  func(msg string) {},
			Warn:  func(msg string) {},
			Error: func(msg string) {},
			Debug: func(msg string) {},
		},
		// No CallTool handler
	}

	def := PluginDef{ID: "test", Name: "Test", Entry: "plugin.js"}
	bridge := NewJSBridge(def, ctx)

	jsCode := `goa.callTool("any", {});`
	result, err := bridge.vm.RunString(jsCode)
	if err != nil {
		t.Fatalf("JS execution error: %v", err)
	}
	if result.String() != "error: CallToolHandler not configured" {
		t.Errorf("expected error message, got %q", result.String())
	}
}
