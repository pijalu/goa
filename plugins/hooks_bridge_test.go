// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"context"

	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
)

// hooksTestEnv loads a fixture JS plugin that registers hooks through
// goa.registerHook, mirroring the quota harness pattern: all goa.* bridges
// mocked, a real HookRegistry fed by RegisterHook, and a real HookSink so
// payload mutations/denies can be driven end-to-end (JS handler → JSON round
// trip → fold → decision).
type hooksTestEnv struct {
	mu       sync.Mutex
	outputs  []string
	warns    []string
	registry *HookRegistry
	bridge   *JSBridge // set by load
	sink     *HookSink
}

const hooksFixtureID = "hook-fixture"

func newHooksTestEnv(tb testing.TB) *hooksTestEnv {
	tb.Helper()
	e := &hooksTestEnv{
		registry: NewHookRegistry(nil),
	}
	e.sink = NewHookSink(e.registry, LoggerAPI{
		Warn:  func(msg string) { e.mu.Lock(); e.warns = append(e.warns, msg); e.mu.Unlock() },
		Debug: func(string) {},
	})
	tb.Cleanup(e.sink.Close)
	return e
}

// context builds the PluginContext routing goa.* through the mocks.
func (e *hooksTestEnv) context() PluginContext {
	noop := func(string) {}
	return PluginContext{
		Config: map[string]any{},
		Logger: LoggerAPI{Info: noop, Error: noop,
			Warn:  func(msg string) { e.mu.Lock(); e.warns = append(e.warns, msg); e.mu.Unlock() },
			Debug: noop,
		},
		RegisterHook: func(spec HookSpec, handler HookHandler) error {
			return e.registry.Register(spec, handler)
		},
		Extended: &ExtendContext{
			Scheduler: NewScheduler(), // plugin timers (unused by the fixture)
			Output: func(msg string) {
				e.mu.Lock()
				e.outputs = append(e.outputs, msg)
				e.mu.Unlock()
			},
		},
	}
}

// hooksFixtureJS registers three hooks covering the API surface: an
// interceptor that mutates, an interceptor that denies, and a notify observer.
const hooksFixtureJS = `
goa.registerHook({
  name: "upper",
  point: "message:pre-send",
  mode: "intercept",
  priority: 10,
  handler: function(p) {
    if (!p.text) return undefined;
    return { text: p.text.toUpperCase() };
  }
});

goa.registerHook({
  name: "guard-rm",
  point: "tool-call:pre",
  mode: "intercept",
  priority: 0,
  handler: function(p) {
    if (p.input && p.input.indexOf("rm -rf") !== -1) {
      return { deny: true, reason: "no destructive commands" };
    }
  }
});

goa.registerHook({
  name: "audit",
  point: "tool-call:post",
  mode: "notify",
  handler: function(p) {
    goa.output("AUDIT:" + p.tool);
  }
});
`

// load writes the fixture manifest + script and loads the plugin.
func (e *hooksTestEnv) load(tb testing.TB) {
	tb.Helper()
	dir := tb.TempDir()
	manifest := "id: " + hooksFixtureID + "\nname: Hook Fixture\nversion: 0.1.0\nentry: plugin.js\n"
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(manifest), 0o600); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.js"), []byte(hooksFixtureJS), 0o600); err != nil {
		tb.Fatal(err)
	}
	bridge, err := LoadFrom(dir, e.context())
	if err != nil {
		tb.Fatalf("LoadFrom hook fixture: %v", err)
	}
	e.bridge = bridge
}

// evalString evaluates a JS expression returning a string, under the VM lock.
func (e *hooksTestEnv) evalString(t *testing.T, expr string) string {
	t.Helper()
	unlock := lockVM()
	defer unlock()
	v, err := e.bridge.vm.RunString(expr)
	if err != nil {
		t.Fatalf("eval %q: %v", expr, err)
	}
	return v.String()
}

func (e *hooksTestEnv) lastOutput() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.outputs) == 0 {
		return ""
	}
	return e.outputs[len(e.outputs)-1]
}

func (e *hooksTestEnv) warnCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.warns)
}

// waitOutput polls until lastOutput() equals want or the deadline passes.
func (e *hooksTestEnv) waitOutput(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if e.lastOutput() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("output %q not observed within 5s (last=%q)", want, e.lastOutput())
}

func TestHooksBridgeRegisterHookEndToEnd(t *testing.T) {
	env := newHooksTestEnv(t)
	env.load(t)

	if got := env.registry.Count(); got != 3 {
		t.Fatalf("registered hooks = %d, want 3", got)
	}
	// The bridge stamps PluginID from its own manifest.
	for _, entry := range env.registry.Snapshot("message:pre-send") {
		if entry.Spec.PluginID != hooksFixtureID {
			t.Fatalf("spec PluginID = %q, want %q", entry.Spec.PluginID, hooksFixtureID)
		}
	}
	ctx := context.Background()

	// Mutation: the JS interceptor uppercases the user input.
	decision, result, reason := env.sink.Intercept(ctx, agentic.HookMessagePreSend,
		map[string]any{"text": "hello world"})
	if decision != agentic.HookModified || reason != "" {
		t.Fatalf("decision/reason = %v/%q, want Modified/\"\"", decision, reason)
	}
	if got := result["text"]; got != "HELLO WORLD" {
		t.Fatalf("mutated text = %v, want HELLO WORLD", got)
	}

	// Deny: the guard vetoes destructive shell inputs.
	decision, _, reason = env.sink.Intercept(ctx, agentic.HookToolCallPre,
		map[string]any{"tool": "bash", "input": "rm -rf /"})
	if decision != agentic.HookDenied || reason != "no destructive commands" {
		t.Fatalf("deny decision/reason = %v/%q", decision, reason)
	}
	// …and lets benign inputs pass.
	decision, _, _ = env.sink.Intercept(ctx, agentic.HookToolCallPre,
		map[string]any{"tool": "bash", "input": "ls -la"})
	if decision != agentic.HookPass {
		t.Fatalf("benign decision = %v, want Pass", decision)
	}
	if wc := env.warnCount(); wc != 0 {
		t.Fatalf("unexpected hook warnings: %v", env.warns)
	}
}

func TestHooksBridgeNotifyDeliversThroughScheduler(t *testing.T) {
	env := newHooksTestEnv(t)
	env.load(t)

	env.sink.Notify(agentic.HookToolCallPost, map[string]any{"tool": "bash", "output": "done"})
	env.waitOutput(t, "AUDIT:bash")
}

func TestHooksBridgeValidationErrors(t *testing.T) {
	env := newHooksTestEnv(t)
	env.load(t)
	cases := []struct {
		name   string
		js     string
		errSub string
	}{
		{"unknown-point", `{name:"x", point:"totally-bogus", mode:"notify", handler:function(){}}`, "unknown hook point"},
		{"bad-mode", `{name:"x", point:"reply:delta", mode:"both", handler:function(){}}`, `mode must be`},
		{"missing-handler", `{name:"x", point:"reply:delta", mode:"notify"}`, "handler"},
		{"non-function-handler", `{name:"x", point:"reply:delta", mode:"notify", handler:42}`, "handler"},
		{"missing-name", `{point:"reply:delta", mode:"notify", handler:function(){}}`, "name is required"},
		{"duplicate-name", `{name:"upper", point:"reply:delta", mode:"notify", handler:function(){}}`, "already registered"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := env.evalString(t, "goa.registerHook("+tc.js+")")
			if !strings.HasPrefix(got, "error:") || !strings.Contains(got, tc.errSub) {
				t.Fatalf("registerHook returned %q, want error containing %q", got, tc.errSub)
			}
		})
	}
	// A well-formed registration still succeeds after the failures above.
	got := env.evalString(t, `goa.registerHook({name:"extra", point:"llm:error", mode:"intercept", handler:function(p){ return {note:"n"} }})`)
	if !strings.Contains(got, "hook registered: extra@llm:error") {
		t.Fatalf("valid registration returned %q", got)
	}
	if !env.registry.HasInterceptors("llm:error") {
		t.Fatal("llm:error intercept not visible in registry")
	}
}

// --- §3.6 M2 gate: 1k synthetic deltas, 0 hooks vs 1 notify vs 1 intercept --

// benchEnv loads the fixture once and registers an extra reply:delta hook
// from the given JS definition ("" = leave the fixture hooks only).
func benchEnv(b *testing.B, extraHookJS string) (*hooksTestEnv, *HookSink) {
	b.Helper()
	env := newHooksTestEnv(b)
	env.load(b)
	if extraHookJS != "" {
		unlock := lockVM()
		_, err := env.bridge.vm.RunString(extraHookJS)
		unlock()
		if err != nil {
			b.Fatalf("register bench hook: %v", err)
		}
	}
	return env, env.sink
}

const benchDeltaHookNotify = `
goa.registerHook({ name: "bench-note", point: "reply:delta", mode: "notify",
  handler: function(p) {} });
`

const benchDeltaHookIntercept = `
goa.registerHook({ name: "bench-redact", point: "reply:delta", mode: "intercept",
  handler: function(p) { return { delta: "[[X]]" }; } });
`

// BenchmarkHookSinkReplyDeltaBursts drives 1000 synthetic content deltas per
// iteration at reply:delta — the M2 perf gate. The notify variant must be
// indistinguishable from baseline (delivery is async); intercept pays the
// synchronous VM call by design.
func BenchmarkHookSinkReplyDeltaBursts(b *testing.B) {
	cases := []struct {
		name string
		hook string
	}{
		{"zero_hooks", ""},
		{"one_notify_js", benchDeltaHookNotify},
		{"one_intercept_js", benchDeltaHookIntercept},
	}
	payload := map[string]any{"point": "reply:delta", "delta": "word ", "is_delta": true, "state": "content"}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			_, sink := benchEnv(b, tc.hook)
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for j := 0; j < 1000; j++ {
					d, _, _ := sink.Intercept(ctx, agentic.HookReplyDelta, payload)
					_ = d
				}
			}
		})
	}
}
