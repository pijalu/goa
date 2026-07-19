// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// quotaTestEnv loads the real provider-quota plugin from the source tree with
// all goa.* bridges mocked, so tests can drive the command, segment, and
// fetchers without network or a TUI.
type quotaTestEnv struct {
	mu         sync.Mutex
	responders []quotaResponder
	outputs    []string
	browserURLs []string
	commands   map[string]func([]string) (string, error)
	segments   *UIBridge
	hotkeys    *HotkeyBridge
	storage    *StorageBridge
	config     map[string]any
}

type quotaResponder struct {
	substr  string
	status  int
	body    string
	headers map[string]string
}

func newQuotaTestEnv(t *testing.T) *quotaTestEnv {
	t.Helper()
	st, err := NewStorageBridge(t.TempDir(), "provider-quota")
	if err != nil {
		t.Fatal(err)
	}
	return &quotaTestEnv{
		commands: map[string]func([]string) (string, error){},
		segments: NewUIBridge(),
		hotkeys:  NewHotkeyBridge(),
		storage:  st,
		config: map[string]any{
			"providers":      map[string]any{},
			"activeProvider": "anthropic",
		},
	}
}

// setActiveProvider sets the active provider id in the mocked goa.config().
func (e *quotaTestEnv) setActiveProvider(id string) {
	e.config["activeProvider"] = id
}

// respond registers a canned JSON response for any URL containing substr.
func (e *quotaTestEnv) respond(substr string, status int, body string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.responders = append(e.responders, quotaResponder{substr: substr, status: status, body: body, headers: map[string]string{}})
}

// setProvider configures a provider in the mocked goa.config().providers.
func (e *quotaTestEnv) setProvider(configID string, p map[string]any) {
	e.config["providers"].(map[string]any)[configID] = p
}

// context builds the PluginContext routing goa.* through the mocks.
func (e *quotaTestEnv) context() PluginContext {
	noop := func(string) {}
	return PluginContext{
		Config: e.config,
		Logger: LoggerAPI{noop, noop, noop, noop},
		RegisterCommand: func(name string, aliases []string, shortHelp, longHelp string, run func([]string) (string, error)) error {
			e.commands[name] = run
			return nil
		},
		Extended: &ExtendContext{
			HTTP:      NewHTTPBridge(),
			Storage:   e.storage,
			Scheduler: NewScheduler(),
			Browser: &BrowserBridge{open: func(u string) error {
				e.mu.Lock()
				e.browserURLs = append(e.browserURLs, u)
				e.mu.Unlock()
				return nil
			}},
			Hotkeys:   e.hotkeys,
			UI:        e.segments,
			Output:    func(m string) { e.mu.Lock(); e.outputs = append(e.outputs, m); e.mu.Unlock() },
			SessionUsage: func() map[string]any {
				return map[string]any{"input": 142300, "output": 28900, "turns": 15, "toolCalls": 20}
			},
			// Named colors so tests can assert the semantic color a segment
			// requests: each name maps to a distinct hex the test greps for
			// (ok=#3fb950, warn=#d29922, critical=#f85149, pending=#8b949e).
			SegmentColor: func(name string) string {
				return map[string]string{
					"ok": "#3fb950", "warn": "#d29922", "critical": "#f85149", "pending": "#8b949e",
				}[name]
			},
		},
	}
}

// load installs the mock httpDo, loads the plugin from disk, and restores.
func (e *quotaTestEnv) load(t *testing.T) *JSBridge {
	t.Helper()
	orig := httpDo
	httpDo = func(b *HTTPBridge, req HTTPRequest) HTTPResponse {
		e.mu.Lock()
		defer e.mu.Unlock()
		for _, r := range e.responders {
			if strings.Contains(req.URL, r.substr) {
				return HTTPResponse{Status: r.status, Body: r.body, Headers: r.headers}
			}
		}
		return HTTPResponse{Status: 404, Body: `{"error":"no mock for ` + req.URL + `"}`, Headers: map[string]string{}}
	}
	t.Cleanup(func() { httpDo = orig })

	bridge, err := LoadFrom(quotaPluginDir, e.context())
	if err != nil {
		t.Fatalf("LoadFrom provider-quota: %v", err)
	}
	return bridge
}

// callCommand runs a registered plugin command and returns its output.
func (e *quotaTestEnv) callCommand(name string, args ...string) string {
	run, ok := e.commands[name]
	if !ok {
		return "COMMAND-NOT-REGISTERED:" + name
	}
	out, err := run(args)
	if err != nil {
		return "ERROR:" + err.Error()
	}
	return out
}

// renderSegment evaluates the quota status segment.
func (e *quotaTestEnv) renderSegment() string {
	for _, s := range e.segments.Segments() {
		if s.ID == "quota" && s.Render != nil {
			return s.Render()
		}
	}
	return ""
}

// lastOutput returns the most recent goa.output message.
func (e *quotaTestEnv) lastOutput() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.outputs) == 0 {
		return ""
	}
	return e.outputs[len(e.outputs)-1]
}

// lastBrowserURL returns the most recent URL passed to goa.openBrowser.
func (e *quotaTestEnv) lastBrowserURL() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.browserURLs) == 0 {
		return ""
	}
	return e.browserURLs[len(e.browserURLs)-1]
}

// hotkeyDef returns the first registered hotkey.
func (e *quotaTestEnv) hotkeyDef() (HotkeyDef, bool) {
	defs := e.hotkeys.Registered()
	if len(defs) == 0 {
		return HotkeyDef{}, false
	}
	return defs[0], true
}

// mockDo returns an httpDo-compatible func resolving this env's canned
// responses, for tests that call fetchers directly (bypassing env.load).
func (e *quotaTestEnv) mockDo() func(*HTTPBridge, HTTPRequest) HTTPResponse {
	return func(b *HTTPBridge, req HTTPRequest) HTTPResponse {
		e.mu.Lock()
		defer e.mu.Unlock()
		for _, r := range e.responders {
			if strings.Contains(req.URL, r.substr) {
				return HTTPResponse{Status: r.status, Body: r.body, Headers: r.headers}
			}
		}
		return HTTPResponse{Status: 404, Body: `{"error":"no mock"}`, Headers: map[string]string{}}
	}
}

// readFileUnder reads a file inside the quota plugin dir.
func readFileUnder(root, rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(root, rel))
}

// formatJS holds the format.js source, exposed as a callable string (the
// module assigns to exports.*, which we surface as globals for tests).
var formatJS = `
var exports = {};
` + mustReadSource("lib/format.js") + `
var tokens = exports.tokens, bar = exports.bar, pct = exports.pct,
    humanize = exports.humanize, pad = exports.pad, cost = exports.cost;
`

func mustReadSource(rel string) string {
	data, err := os.ReadFile(filepath.Join(quotaPluginDir, rel))
	if err != nil {
		panic("read " + rel + ": " + err.Error())
	}
	return string(data)
}
