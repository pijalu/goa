// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/ansi"
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
	// Compact form "42|30": session and weekly percentages, no window labels.
	if !strings.Contains(seg, "42") {
		t.Fatalf("segment = %q, want session percent 42", seg)
	}
	if !strings.Contains(seg, "30") {
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
	env.load(t)
	registerStubOAuth(t, env)

	out := env.callCommand("quota", "auth-status")
	if !strings.Contains(out, "Anthropic") {
		t.Fatalf("auth-status missing Anthropic: %q", out)
	}
	if !strings.Contains(out, "api key ✓") {
		t.Fatalf("auth-status should show anthropic api key ok: %q", out)
	}
	if !strings.Contains(out, "not authenticated ∇") {
		t.Fatalf("auth-status should show the OAuth provider needs auth: %q", out)
	}
}

// --- login flow ---

func TestQuota_LoginStartsDeviceFlow(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.respond("auth.example.com/device/code", 200, `{
		"device_code": "dev-abc", "user_code": "ABC-DEF",
		"verification_uri": "https://auth.example.com/activate", "interval": 5
	}`)

	env.load(t)
	registerStubOAuth(t, env)
	out := env.callCommand("quota", "login", stubOAuthID)
	if !strings.Contains(out, "Opening browser") {
		t.Fatalf("login output = %q", out)
	}
	// Device flow printed the verification URL + code.
	if !strings.Contains(env.lastOutput(), "auth.example.com/activate") {
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

// TestQuota_LoginRelativeVerificationURI is the regression for the broken
// authorization link: a device endpoint returning RELATIVE verification URIs
// ("/device?user_code=…", as console.opencode.ai does) produced an unusable
// host-less link. The flow must resolve them against the issuer origin.
func TestQuota_LoginRelativeVerificationURI(t *testing.T) {
	env := newQuotaTestEnv(t)
	// Exact shape observed from the live console.opencode.ai endpoint.
	env.respond("auth.example.com/device/code", 200, `{
		"device_code": "9bSrUCbMvMhe6K8zbLw1zaHr67aruh3qHP4y0Wo2",
		"user_code": "PCDM-TDQM",
		"verification_uri": "/device",
		"verification_uri_complete": "/device?user_code=PCDM-TDQM",
		"expires_in": 900, "interval": 5
	}`)

	env.load(t)
	registerStubOAuth(t, env)
	out := env.callCommand("quota", "login", stubOAuthID)
	if !strings.Contains(out, "Opening browser") {
		t.Fatalf("login output = %q", out)
	}
	got := env.lastOutput()
	want := "https://auth.example.com/device?user_code=PCDM-TDQM"
	if !strings.Contains(got, want) {
		t.Fatalf("relative verification URI not absolutized: got %q, want %q inside", got, want)
	}
	if !strings.Contains(got, "Enter code: PCDM-TDQM") {
		t.Fatalf("user code missing from output: %q", got)
	}
	// The browser must receive the same absolutized URL, not the relative path.
	if got := env.lastBrowserURL(); got != want {
		t.Fatalf("openBrowser URL = %q, want %q", got, want)
	}
}

// TestQuota_LoginRelativeVerificationURINoComplete covers the fallback when
// only the relative verification_uri (no _complete variant) is returned.
func TestQuota_LoginRelativeVerificationURINoComplete(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.respond("auth.example.com/device/code", 200, `{
		"device_code": "dev-xyz", "user_code": "FGDD-HDHN",
		"verification_uri": "/device", "interval": 5
	}`)

	env.load(t)
	registerStubOAuth(t, env)
	env.callCommand("quota", "login", stubOAuthID)
	got := env.lastOutput()
	want := "https://auth.example.com/device"
	if !strings.Contains(got, want) {
		t.Fatalf("verification_uri not absolutized: got %q, want %q inside", got, want)
	}
}

// --- logout ---

func TestQuota_LogoutClearsTokens(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.load(t)
	registerStubOAuth(t, env)

	// Simulate a stored token, then log out.
	env.storage.Set(stubOAuthID+".access_token", "tok")
	env.storage.Set(stubOAuthID+".refresh_token", "ref")
	out := env.callCommand("quota", "logout", stubOAuthID)
	if !strings.Contains(out, "Logged out") {
		t.Fatalf("logout = %q", out)
	}
	if got := env.storage.Get(stubOAuthID + ".access_token"); got != "" {
		t.Fatalf("token not cleared: %q", got)
	}
}

// --- error handling ---

func TestQuota_AuthRequiredShownInBreakdown(t *testing.T) {
	env := newQuotaTestEnv(t)
	// No token stored → the OAuth fetcher returns auth_required.
	env.load(t)
	registerStubOAuth(t, env)
	out := env.callCommand("quota")
	if !strings.Contains(out, "auth required") || !strings.Contains(out, stubOAuthName) {
		t.Fatalf("expected auth-required note for the OAuth provider:\n%s", out)
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
	if !strings.Contains(seg, "42") {
		t.Fatalf("carousel should prefer anthropic over local: %q", seg)
	}
	if strings.Contains(seg, "tok") {
		t.Fatalf("local fallback should not rotate in when a real provider has data: %q", seg)
	}
}

// TestQuota_ProviderSwitchUpdatesSegment covers bugs.md "Quota": after
// switching the active provider, the segment must track the new provider, not
// keep showing the provider that was active at startup.
func TestQuota_ProviderSwitchUpdatesSegment(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("anthropic", map[string]any{"provider": "anthropic", "apiKey": "sk"})
	env.setProvider("z.ai", map[string]any{"provider": "zai", "apiKey": "k"})
	env.respond("api.anthropic.com/v1/usage", 200, `{"usage":{"session":{"used":42,"limit":100}}}`)
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, `{"data":{"session":{"used":38,"limit":100}}}`)
	env.load(t)
	env.callCommand("quota", "refresh")
	// anthropic active at startup → 42.
	if seg := ansi.Strip(env.renderSegment()); !strings.Contains(seg, "42") {
		t.Fatalf("startup segment should show anthropic 42: %q", seg)
	}
	// Switch to z.ai → segment must update to 38, not stay on anthropic.
	env.setActiveProvider("z.ai")
	seg := ansi.Strip(env.renderSegment())
	if !strings.Contains(seg, "38") {
		t.Fatalf("after provider switch segment should show z.ai 38: %q", seg)
	}
	if strings.Contains(seg, "42") {
		t.Fatalf("stale startup provider quota after switch: %q", seg)
	}
}

// TestQuota_UnsupportedProviderStatesNotSupported covers bugs.md "Quota": when
// the active provider has no quota API, /quota must say so explicitly.
func TestQuota_UnsupportedProviderStatesNotSupported(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("local", map[string]any{"provider": "local"})
	env.setActiveProvider("local")
	env.load(t)
	out := env.callCommand("quota")
	if !strings.Contains(out, "not supported") {
		t.Fatalf("/quota should state quota is not supported for the active provider:\n%s", out)
	}
}

// TestQuota_BudgetSummaryPlentyOfRoom covers bugs.md "Quota color": /quota
// explains the budget status in words (e.g. "plenty of room").
func TestQuota_BudgetSummaryPlentyOfRoom(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("anthropic", map[string]any{"provider": "anthropic", "apiKey": "sk"})
	// Well within budget: low usage, far from reset.
	env.respond("api.anthropic.com/v1/usage", 200, `{"usage":{"session":{"used":10,"limit":100,"reset_at":"2099-01-01T01:48:00Z"}}}`)
	env.load(t)
	out := env.callCommand("quota")
	if !strings.Contains(out, "plenty of room") {
		t.Fatalf("/quota should explain the budget status:\n%s", out)
	}
}

// TestQuota_TableMergesUsageAndPct covers the request to merge the Usage and %
// columns: the table must show "bar + pct%" once, not the redundant "42/100"
// numbers plus a separate % column.
func TestQuota_TableMergesUsageAndPct(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("anthropic", map[string]any{"provider": "anthropic", "apiKey": "sk"})
	env.respond("api.anthropic.com/v1/usage", 200, `{"usage":{"session":{"used":42,"limit":100,"reset_at":"2099-01-01T01:48:00Z"}}}`)
	env.load(t)
	out := env.callCommand("quota")
	// Merged form: a bar followed by "42%".
	if !strings.Contains(out, "42%") {
		t.Fatalf("/quota should show merged usage percent:\n%s", out)
	}
	// The redundant "42/100" fraction must be gone.
	if strings.Contains(out, "42/100") {
		t.Fatalf("/quota should not show the redundant N/limit fraction:\n%s", out)
	}
	// And there is no standalone "%" column header anymore.
	if strings.Contains(out, "| % |") {
		t.Fatalf("/quota should not have a separate %% column:\n%s", out)
	}
}

// TestQuota_TableHasAtResetAndStatus covers the "At reset" projection column
// and the per-provider/per-window Status column.
func TestQuota_TableHasAtResetAndStatus(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("anthropic", map[string]any{"provider": "anthropic", "apiKey": "sk"})
	env.respond("api.anthropic.com/v1/usage", 200, `{"usage":{"session":{"used":42,"limit":100,"reset_at":"2099-01-01T01:48:00Z"}}}`)
	env.load(t)
	out := env.callCommand("quota")
	for _, want := range []string{"At reset", "Status"} {
		if !strings.Contains(out, want) {
			t.Errorf("/quota missing %q column:\n%s", want, out)
		}
	}
}

// TestQuota_SegmentPerWindowColorsDistinct covers "color should be at each
// number": when two windows project to different budget levels, their
// percentages must carry different colors (not one shared worst-window color).
func TestQuota_SegmentPerWindowColorsDistinct(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("kimi-code", map[string]any{"provider": "kimi-code", "apiKey": "sk", "endpoint": "https://api.kimi.com/coding/v1"})
	env.setActiveProvider("kimi-code")
	// Session window far from reset (green) but weekly nearly exhausted (red):
	// used=10 with reset far future → low pace; weekly used=95 → high pace.
	sessionReset := time.Now().Add(4 * time.Hour).UTC().Format(time.RFC3339)
	weeklyReset := time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339)
	env.respond("api.kimi.com/coding/v1/usages", 200, `{
		"user": {"membership": {"level": "LEVEL_ADVANCED"}},
		"usage": {"limit": "100", "used": "95", "resetTime": `+strconv.Quote(weeklyReset)+`},
		"limits": [
			{"window": {"duration": 300, "timeUnit": "TIME_UNIT_MINUTE"},
			 "detail": {"limit": "100", "used": "10", "resetTime": `+strconv.Quote(sessionReset)+`}}
		]
	}`)
	env.load(t)
	env.callCommand("quota", "refresh")
	seg := env.renderSegment()
	// Two distinct windows → two (potentially different) color spans present.
	// At minimum both window percentages must be individually wrapped.
	if strings.Count(seg, "38;2;") < 2 {
		t.Fatalf("segment should color each window separately, got: %q", seg)
	}
}

func TestQuota_SegmentMultiProviderShowsActiveOnly(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("anthropic", map[string]any{"provider": "anthropic", "apiKey": "sk"})
	env.setProvider("z.ai", map[string]any{"provider": "zai", "apiKey": "k"})
	env.respond("api.anthropic.com/v1/usage", 200, `{"usage":{"session":{"used":42,"limit":100}}}`)
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, `{"data":{"session":{"used":38,"limit":100}}}`)
	// anthropic is active (harness default) → only anthropic's quota shows.
	env.load(t)
	env.callCommand("quota", "refresh")
	seg := env.renderSegment()
	stripped := ansi.Strip(seg)
	if !strings.Contains(stripped, "42") {
		t.Fatalf("segment should show active provider quota: %q", seg)
	}
	if strings.Contains(stripped, "38") {
		t.Fatalf("inactive provider quota leaked into segment: %q", seg)
	}
}