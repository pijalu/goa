package plugins

// M6 §7 capability-gating tests: manifest permissions gate sensitive JS
// APIs. Both gates fail closed with the documented error shapes.

import (
	"context"
	"strings"
	"testing"
)

// TestUIConfirmJS_RequiresPermission proves goa.ui.confirm is denied when the
// bridge's manifest does not declare the ui-confirm permission.
func TestUIConfirmJS_RequiresPermission(t *testing.T) {
	b, ui := confirmJSEnvNoPerm(t)
	ui.SetConfirmConsumer(true) // UI available, so denial is purely permission-based

	spec := `{title:"t", body:"b", options:[{id:"yes",label:"Yes"}]}`
	done := runConfirmJS(t, b, spec)
	if err := <-done; err != nil {
		t.Fatalf("RunString: %v", err)
	}
	res := readConfirmJSON(t, b)

	if !strings.Contains(res, "ui-confirm") || !strings.Contains(res, "permission") {
		t.Fatalf("result = %s, want ui-confirm permission error", res)
	}
	// Fail-closed contract: nothing may reach the confirm queue.
	select {
	case <-ui.ConfirmRequests():
		t.Fatalf("confirm reached queue despite missing permission")
	default:
	}
}

func TestOAuthTokenJS_RequiresPermission(t *testing.T) {
	ctx := newExtendedContext(t, t.TempDir(), NewHTTPBridge())
	ctx.Extended.OAuthToken = func(_ context.Context, provider string) (map[string]any, error) {
		return map[string]any{"accessToken": "should-not-leak"}, nil
	}
	bridge := NewJSBridge(PluginDef{ID: "test", Entry: "plugin.js"}, ctx) // no permissions
	unlock := lockVM()
	_, err := bridge.vm.RunString(`goa.__res = JSON.stringify(goa.auth.oauthToken("openai"))`)
	unlock()
	if err != nil {
		t.Fatalf("RunString: %v", err)
	}
	got := goaResult(t, bridge, "__res").String()
	if strings.Contains(got, "should-not-leak") {
		t.Fatalf("token leaked without oauth-token permission: %s", got)
	}
	if !strings.Contains(got, "oauth-token") {
		t.Fatalf("result = %s, want oauth-token permission error", got)
	}
}

func TestHasPermission(t *testing.T) {
	b := &JSBridge{def: PluginDef{Permissions: []string{"provider-keys", "ui-confirm"}}}
	for perm, want := range map[string]bool{
		"provider-keys": true,
		"ui-confirm":    true,
		"oauth-token":   false,
		"account-write": false,
	} {
		if got := b.hasPermission(perm); got != want {
			t.Errorf("hasPermission(%q) = %v, want %v", perm, got, want)
		}
	}
}

// confirmJSEnvNoPerm mirrors confirmJSEnv but without any declared
// permissions (negative fixture).
func confirmJSEnvNoPerm(t *testing.T) (*JSBridge, *UIBridge) {
	t.Helper()
	ui := NewUIBridge()
	noop := func(string) {}
	bridge := NewJSBridge(PluginDef{ID: "no-perm-fixture", Entry: "plugin.js"}, PluginContext{
		Config: map[string]any{},
		Logger: LoggerAPI{Info: noop, Warn: noop, Error: noop, Debug: noop},
		Extended: &ExtendContext{
			UI:        ui,
			Scheduler: NewScheduler(),
			Output:    noop,
		},
	})
	return bridge, ui
}
