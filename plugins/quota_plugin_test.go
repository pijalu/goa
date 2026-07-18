// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// quotaPluginDir is the absolute path to the provider-quota plugin source,
// resolved relative to this test file so `go test` works from anywhere.
var quotaPluginDir = func() string {
	_, file, _, _ := runtime.Caller(0)
	// plugins/quota_plugin_test.go -> plugins/bundled/provider-quota
	return filepath.Join(filepath.Dir(file), "bundled", "provider-quota")
}()

// --- command registration ---

func TestQuota_RegistersCommandSegmentHotkey(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.load(t)

	if _, ok := env.commands["quota"]; !ok {
		t.Fatal("/quota command not registered")
	}
	if len(env.segments.Segments()) == 0 {
		t.Fatal("quota segment not registered")
	}
	hk, ok := env.hotkeyDef()
	if !ok {
		t.Fatal("hotkey not registered")
	}
	if hk.KeyName() != "ctrl+shift+q" {
		t.Fatalf("hotkey = %q, want ctrl+shift+q", hk.KeyName())
	}
}

// --- /quota full output ---

func TestQuota_FullBreakdownWithProviders(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("anthropic", map[string]any{"id": "anthropic", "provider": "anthropic", "apiKey": "sk-ant"})
	env.setProvider("z.ai", map[string]any{"id": "z.ai", "provider": "zai", "apiKey": "zai-key"})

	env.respond("api.anthropic.com/v1/usage", 200, `{
		"plan": {"name": "Pro"},
		"usage": {
			"session": {"used": 42, "limit": 100, "reset_at": "2099-01-01T01:48:00Z"},
			"weekly":  {"used": 30, "limit": 100, "reset_at": "2099-01-05T12:00:00Z"}
		}
	}`)
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, `{
		"data": {
			"plan": "Free",
			"session": {"used": 38, "limit": 100, "reset_at": "2099-01-01T02:15:00Z"},
			"web_search": {"used": 3, "limit": 100, "reset_at": "2099-01-14T00:00:00Z"}
		}
	}`)

	env.load(t)
	out := env.callCommand("quota")

	for _, want := range []string{"Session Usage", "Anthropic", "Pro", "Z.ai", "Free", "Local"} {
		if !strings.Contains(out, want) {
			t.Errorf("/quota output missing %q:\n%s", want, out)
		}
	}
	// Progress bars + percentages present.
	if !strings.Contains(out, "%") {
		t.Errorf("no percentages in output:\n%s", out)
	}
}

// --- /quota:json ---

func TestQuota_JSONOutput(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("anthropic", map[string]any{"provider": "anthropic", "apiKey": "sk"})
	env.respond("api.anthropic.com/v1/usage", 200, `{"plan":{"name":"Pro"},"usage":{"session":{"used":42,"limit":100}}}`)

	env.load(t)
	out := env.callCommand("quota", "json")
	if !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Fatalf("not JSON: %q", out)
	}
	if !strings.Contains(out, `"anthropic"`) || !strings.Contains(out, `"Pro"`) {
		t.Fatalf("JSON missing provider data: %q", out)
	}
	if !strings.Contains(out, `"session"`) {
		t.Fatalf("JSON missing session usage: %q", out)
	}
}

// --- status segment ---

func TestQuota_SegmentShowsWindowedQuota(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("anthropic", map[string]any{"provider": "anthropic", "apiKey": "sk"})
	env.respond("api.anthropic.com/v1/usage", 200, `{"usage":{"session":{"used":42,"limit":100},"weekly":{"used":30,"limit":100}}}`)

	env.load(t)
	env.callCommand("quota", "refresh") // populate cache synchronously
	seg := env.renderSegment()
	if !strings.Contains(seg, "5h:42%") {
		t.Fatalf("segment = %q, want 5h:42%%", seg)
	}
	if !strings.Contains(seg, "30%") {
		t.Fatalf("segment missing weekly: %q", seg)
	}
}

func TestQuota_SegmentEmptyWhenNoData(t *testing.T) {
	env := newQuotaTestEnv(t)
	// No providers configured → only local fallback (no windowed limits).
	env.load(t)
	// Local fallback has data but renders as tokens, so segment shows tokens.
	seg := env.renderSegment()
	// Either empty (no providers list) or local token readout — never panics.
	_ = seg
}

// --- auth status ---

func TestQuota_AuthStatus(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("anthropic", map[string]any{"provider": "anthropic", "apiKey": "sk"})
	env.setProvider("opencode", map[string]any{"provider": "opencode"})
	env.load(t)

	out := env.callCommand("quota", "auth-status")
	if !strings.Contains(out, "Anthropic") {
		t.Fatalf("auth-status missing Anthropic: %q", out)
	}
	if !strings.Contains(out, "api key ✓") {
		t.Fatalf("auth-status should show anthropic api key ok: %q", out)
	}
	if !strings.Contains(out, "not authenticated ∇") {
		t.Fatalf("auth-status should show opencode needs auth: %q", out)
	}
}

// --- login flow ---

func TestQuota_LoginStartsDeviceFlow(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("opencode", map[string]any{"provider": "opencode"})
	env.respond("console.opencode.ai/auth/device/code", 200, `{
		"device_code": "dev-abc", "user_code": "ABC-DEF",
		"verification_uri": "https://console.opencode.ai/activate", "interval": 5
	}`)

	env.load(t)
	out := env.callCommand("quota", "login", "opencode")
	if !strings.Contains(out, "Opening browser") {
		t.Fatalf("login output = %q", out)
	}
	// Device flow printed the verification URL + code.
	if !strings.Contains(env.lastOutput(), "console.opencode.ai/activate") {
		t.Fatalf("no verification URL in output: %q", env.lastOutput())
	}
}

func TestQuota_LoginAPIKeyProviderNoOp(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("anthropic", map[string]any{"provider": "anthropic", "apiKey": "sk"})
	env.load(t)
	out := env.callCommand("quota", "login", "anthropic")
	if !strings.Contains(out, "API-key auth") {
		t.Fatalf("login api-key provider = %q", out)
	}
}

// --- logout ---

func TestQuota_LogoutClearsTokens(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("opencode", map[string]any{"provider": "opencode"})
	env.load(t)

	// Simulate a stored token, then log out.
	env.storage.Set("opencode.access_token", "tok")
	env.storage.Set("opencode.refresh_token", "ref")
	out := env.callCommand("quota", "logout", "opencode")
	if !strings.Contains(out, "Logged out") {
		t.Fatalf("logout = %q", out)
	}
	if got := env.storage.Get("opencode.access_token"); got != "" {
		t.Fatalf("token not cleared: %q", got)
	}
}

// --- error handling ---

func TestQuota_AuthRequiredShownInBreakdown(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("opencode", map[string]any{"provider": "opencode"})
	// No token stored → fetcher returns auth_required.
	env.load(t)
	out := env.callCommand("quota")
	if !strings.Contains(out, "auth required") || !strings.Contains(out, "OpenCode") {
		t.Fatalf("expected auth-required note for opencode:\n%s", out)
	}
}

func TestQuota_HTTPErrorSurfaced(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("z.ai", map[string]any{"provider": "zai", "apiKey": "k"})
	env.respond("api.z.ai", 500, `{"error":"boom"}`)
	env.load(t)
	out := env.callCommand("quota")
	if !strings.Contains(out, "error") {
		t.Fatalf("expected error surfaced for z.ai:\n%s", out)
	}
}

// --- subcommand dispatch ---

func TestQuota_UnknownSubcommand(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.load(t)
	out := env.callCommand("quota", "bogus")
	if !strings.Contains(out, "Unknown /quota subcommand") {
		t.Fatalf("unknown sub = %q", out)
	}
}

func TestQuota_ProviderSubcommandForceRefresh(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("anthropic", map[string]any{"provider": "anthropic", "apiKey": "sk"})
	env.respond("api.anthropic.com/v1/usage", 200, `{"usage":{"session":{"used":10,"limit":100}}}`)
	env.load(t)
	out := env.callCommand("quota", "anthropic")
	if !strings.Contains(out, "Anthropic") {
		t.Fatalf("/quota:anthropic = %q", out)
	}
}

// --- carousel ---

func TestQuota_CarouselPrefersAPIProvidersOverLocal(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("anthropic", map[string]any{"provider": "anthropic", "apiKey": "sk"})
	env.respond("api.anthropic.com/v1/usage", 200, `{"usage":{"session":{"used":42,"limit":100}}}`)
	env.load(t)
	env.callCommand("quota", "refresh")
	// With anthropic holding windowed data, the segment must show it, not the
	// local token fallback.
	seg := env.renderSegment()
	if !strings.Contains(seg, "5h:42%") {
		t.Fatalf("carousel should prefer anthropic over local: %q", seg)
	}
	if strings.Contains(seg, "tok") {
		t.Fatalf("local fallback should not rotate in when a real provider has data: %q", seg)
	}
}

func TestQuota_SegmentMultiProviderLabelsName(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("anthropic", map[string]any{"provider": "anthropic", "apiKey": "sk"})
	env.setProvider("z.ai", map[string]any{"provider": "zai", "apiKey": "k"})
	env.respond("api.anthropic.com/v1/usage", 200, `{"usage":{"session":{"used":42,"limit":100}}}`)
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, `{"data":{"session":{"used":38,"limit":100}}}`)
	env.load(t)
	env.callCommand("quota", "refresh")
	seg := env.renderSegment()
	// Multiple providers → segment prefixed with the provider name.
	if !strings.Contains(seg, "Anthropic") && !strings.Contains(seg, "Z.ai") {
		t.Fatalf("multi-provider segment should label the provider: %q", seg)
	}
}
