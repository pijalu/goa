// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic/provider/oauth"
	"github.com/pijalu/goa/internal/agentic/provider/startplan"
	"github.com/pijalu/goa/internal/auth"
	"github.com/pijalu/goa/plugins"
	"github.com/pijalu/goa/provider"
)

// newQuotaFilmstripScenario builds a full-TUI uiScenario with the REAL
// bundled provider-quota plugin loaded through the production load path.
// The provider set is chosen for offline determinism:
//
//   - "anthropic" is configured WITHOUT an API key → its fetcher resolves no
//     token and returns the no_api_key error entry before any HTTP call;
//   - every other provider is unconfigured and omitted without network I/O;
//   - configured OAuth fixtures short-circuit to auth_required when logged out;
//   - the local fallback fetcher reads session counters only.
//
// So the plugin's cache primes itself with zero network I/O, letting the
// test assert on bare /quota's EXTENDED view (Session Usage + Provider
// Quotas tables) deterministically.
func newQuotaFilmstripScenario(t *testing.T, extraProviders ...config.ProviderConfig) *uiScenario {
	t.Helper()
	// 100x44: the extended /quota view (Session Usage + Provider Quotas with
	// the configured offline and Start Plan rows) must fit the viewport for
	// the filmstrip assertions; these rows overflowed the old 30-line frame.
	sc := newUIScenario(t, 100, 44)

	pm := newPluginTestSubsystems(t)
	sc.app.subs.pluginMgr = pm.pluginMgr
	sc.app.subs.toolRegistry = pm.toolRegistry
	sc.app.subs.registry = pm.registry
	// Scenario cfg owns the TUI layout settings; graft the plugin-relevant
	// fields onto it so components keep their captured pointer.
	sc.app.subs.cfg.ConfigDir = pm.cfg.ConfigDir
	sc.app.subs.cfg.Providers = []config.ProviderConfig{
		{ID: "anthropic", Name: "Anthropic", Provider: "anthropic", APIKey: ""},
		// Active local provider → the compact footer segment takes the
		// "[∞]" local path (offline-deterministic).
		{ID: "local-llama", Name: "Local Llama", Provider: "lmstudio", Endpoint: "http://localhost:1234/v1"},
	}
	sc.app.subs.cfg.Providers = append(sc.app.subs.cfg.Providers, extraProviders...)
	sc.app.subs.cfg.ActiveProvider = "local-llama"
	store, err := auth.NewStore("")
	if err != nil {
		t.Fatalf("auth store: %v", err)
	}
	providerMgr := provider.NewProviderManager(sc.app.subs.cfg)
	providerMgr.SetAuthStore(store)
	sc.app.subs.authStore = store
	sc.app.subs.providerMgr = providerMgr

	// This scenario drives PRODUCTION async paths (async slash commands,
	// activatePluginUI's refresh/confirm drains) that spawn goroutines, so it
	// must run the real Actor loops — with the loops down, TUI.Apply degrades
	// to inline execution and those goroutines race the test goroutine
	// instead of serializing through the commandLoop like production.
	sc.engine.RunLoops()

	loadEnabledPlugins(sc.app.subs)
	if sc.app.subs.getPluginRT() == nil {
		t.Fatal("plugin runtime not loaded")
	}
	sc.engine.ApplySync(func() { sc.app.activatePluginUI(sc.engine) })
	subs := sc.app.subs
	subs.cmdRouter = core.NewCommandRouter(subs.registry, core.NewDocEngine(subs.registry))
	return sc
}

// quotaCommandOutput runs /quota through the registry directly (the same
// wrapper the router executes) and returns its rendered markdown.
func quotaCommandOutput(t *testing.T, subs *subsystems, args ...string) string {
	t.Helper()
	cmd, ok := subs.registry.Resolve("quota")
	if !ok {
		t.Fatal("quota command not registered by plugin load")
	}
	var buf strings.Builder
	ctx := core.Context{OutputBuffer: &buf}
	if err := cmd.Run(ctx, args); err != nil {
		t.Fatalf("quota run(%v): %v", args, err)
	}
	return buf.String()
}

// TestQuotaPlugin_ExtendedInfoInTUI_Filmstrip drives bare /quota through the
// real slash-command path and captures the result with the Filmstrip,
// proving the quota plugin's EXTENDED information (session usage table +
// per-provider quota breakdown rows) reaches the visible TUI — not just the
// compact footer segment covered by plugin_segment_filmstrip_test.go.
func TestQuotaPlugin_ExtendedInfoInTUI_Filmstrip(t *testing.T) {
	sc := newQuotaFilmstripScenario(t)
	subs := sc.app.subs

	// Wait out the plugin's setTimeout(0) cache prime so bare /quota takes
	// the instant warm-cache path (deterministic full render).
	waitFor(t, sc.engine, 3*time.Second, func() bool {
		return strings.Contains(quotaCommandOutput(t, subs), "Provider Quotas")
	}, "quota cache never primed (no 'Provider Quotas' in warm render)")

	film := sc.filmstrip()
	sc.engine.RenderNow()
	film.Capture("baseline-before-quota", sc.engine.AgentFrame(), "")

	// Dispatch through handleSlashCommand exactly as a user submit would.
	// /quota is an async-hint command (runs off the loop with a spinner), so
	// poll frames until the extended output is visible before capturing.
	sc.engine.ApplySync(func() { sc.app.handleSlashCommand("/quota") })
	waitFor(t, sc.engine, 3*time.Second, func() bool {
		for _, line := range sc.engine.AgentFrame().Visible {
			if strings.Contains(line, "Provider Quotas") {
				return true
			}
		}
		return false
	}, "/quota extended output never reached the viewport")

	sc.engine.RenderNow()
	snap := film.Capture("quota-extended-info", sc.engine.AgentFrame(), sc.status.Text())

	assertFrameContains(t, snap, "Session Usage")
	assertFrameContains(t, snap, "Provider Quotas")
	assertFrameContains(t, snap, "Anthropic")
	assertFrameContains(t, snap, "no API key")
	for _, unconfigured := range []string{"Codex", "Z.ai Start Plan", "BigModel Start Plan"} {
		if strings.Contains(strings.Join(snap.Frame.Visible, "\n"), unconfigured) {
			t.Errorf("unconfigured provider %q reached visible quota table:\n%s", unconfigured, snap.Frame.Dump())
		}
	}
}

// TestQuotaPlugin_ZaiOAuthThenQuota_Filmstrip drives the production auth
// store, provider manager, bundled plugin, slash-command dispatcher, and
// visible terminal through the reported sequence. The first render caches
// auth_required; persisting the OAuth credential must make the next /quota
// refresh and show the real Start Plan balance without a restart.
func TestQuotaPlugin_ZaiOAuthThenQuota_Filmstrip(t *testing.T) {
	balanceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer zjwt-visible" {
			t.Errorf("Authorization = %q, want login-derived zcode JWT", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Device-Mid") == "" {
			t.Error("Start Plan request omitted X-Device-Mid")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"data": map[string]any{
				"server_time": 1800000000,
				"plans": []any{map[string]any{
					"plan_id": "p1", "user_plan_id": "up1", "status": "active", "name": "Start",
					"starts_at": 1700000000, "ends_at": 1900000000,
					"entitlements": []any{map[string]any{"effective_at": 1700000000}},
				}},
				"balances": []any{map[string]any{
					"bucket_id": "b1", "user_plan_id": "up1", "total_units": 100, "remaining_units": 40,
					"expires_at": 1900000000, "capabilities": []string{"model:glm-visible"},
				}},
			},
		})
	}))
	t.Cleanup(balanceServer.Close)
	startplan.SetDeviceMidDir(t.TempDir())
	t.Cleanup(func() { startplan.SetDeviceMidDir("") })

	sc := newQuotaFilmstripScenario(t, config.ProviderConfig{
		ID: "zai-start-plan", Name: "Z.ai Start Plan", Provider: "zai-start-plan",
		Endpoint: balanceServer.URL + "/api/v1/zcode-plan",
	})
	subs := sc.app.subs
	waitFor(t, sc.engine, 3*time.Second, func() bool {
		return strings.Contains(quotaCommandOutput(t, subs), "auth required")
	}, "logged-out Start Plan state never primed")

	if err := subs.authStore.SetOAuth("zai", &oauth.Tokens{AccessToken: "business-visible", ZcodeJWT: "zjwt-visible"}); err != nil {
		t.Fatalf("persist OAuth login: %v", err)
	}
	sc.engine.ApplySync(func() { sc.app.handleSlashCommand("/quota") })
	// The initial command returns immediately; the refreshed table arrives
	// later through goa.output -> ChatEvent. Drive the same event handlers as
	// commandLoop while waiting for that asynchronous render.
	waitForFrame(t, sc, "60%")
	film := sc.filmstrip()
	snap := film.Capture("zai-oauth-quota-refreshed", sc.engine.AgentFrame(), sc.status.Text())
	visible := strings.Join(snap.Frame.Visible, "\n")
	for _, want := range []string{"Z.ai Start Plan", "glm-visible", "60%"} {
		if !strings.Contains(visible, want) {
			t.Errorf("post-login visible quota missing %q:\n%s", want, snap.Frame.Dump())
		}
	}
	for _, unwanted := range []string{"auth required", "/login:zai:oauth", "BigModel Start Plan", "Codex"} {
		if strings.Contains(visible, unwanted) {
			t.Errorf("post-login visible quota contains %q:\n%s", unwanted, snap.Frame.Dump())
		}
	}
	t.Logf("z.ai OAuth → quota filmstrip:\n%s", film.Render())
}

// TestQuotaPlugin_SegmentShowsCompactWhileExtendedAvailable pins the pairing
// the feature promises: once the cache holds data, the footer segment shows
// the COMPACT status while the command output carries the EXTENDED breakdown.
//
// The poll (not a single-shot assert) mirrors the production contract, not a
// timing assumption: a segment render that lands while the plugin VM is busy
// (the setTimeout(0) cache-prime frame) is SKIPPED — buildSegmentRender
// reports ok=false and pushPluginSegments keeps the previous text — and the
// prime's own goa.ui.refreshSegment re-renders with data once the frame
// drains. A single immediate push could therefore legally observe the stale
// "[…]" pending render (CI: TestQuotaPlugin flake, 2026-09-03). What is
// pinned: within a bounded window the compact marker ([∞] for the active
// local provider, or a percent pair) MUST reach the footer — and the
// extended table never does.
func TestQuotaPlugin_SegmentShowsCompactWhileExtendedAvailable(t *testing.T) {
	sc := newQuotaFilmstripScenario(t)
	subs := sc.app.subs

	waitFor(t, sc.engine, 3*time.Second, func() bool {
		return strings.Contains(quotaCommandOutput(t, subs), "Provider Quotas")
	}, "quota cache never primed")

	footerHasCompact := func() bool {
		sc.engine.ApplySync(func() { sc.app.pushPluginSegments(sc.engine) })
		sc.engine.RenderNow()
		frame := sc.engine.AgentFrame()
		node := frame.FindNode("Footer")
		if node == nil {
			t.Fatal("Footer missing from frame")
		}
		// Compact segment: either the local "[∞]" marker or a percent pair —
		// never the multi-line extended table.
		return strings.Contains(node.Text, "[∞]") || strings.Contains(node.Text, "%")
	}
	waitFor(t, sc.engine, 3*time.Second, footerHasCompact,
		"footer segment never showed compact quota status after cache prime")
}

// compile-time guard: keeps the plugins import meaningful even if scenario
// helpers change underneath us.
var _ = plugins.UISegmentDef{}
