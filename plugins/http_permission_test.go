// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"strings"
	"testing"
)

// TestHTTPFetchJS_RequiresNetworkPermission pins the network capability gate:
// goa.http.fetch without the "network" manifest permission must fail closed
// with a permission error and never reach the HTTP hook.
func TestHTTPFetchJS_RequiresNetworkPermission(t *testing.T) {
	reachedHook := false
	restore := setHTTPDo(func(b *HTTPBridge, req HTTPRequest) HTTPResponse {
		reachedHook = true
		return HTTPResponse{Status: 200, Body: "should-not-leak"}
	})
	defer restore()

	ctx := newExtendedContext(t, t.TempDir(), NewHTTPBridge())
	bridge := NewJSBridge(PluginDef{ID: "test", Entry: "plugin.js"}, ctx) // no permissions
	unlock := lockVM()
	_, err := bridge.vm.RunString(`goa.__res = JSON.stringify(goa.http.fetch("https://example.com/quota"))`)
	unlock()
	if err != nil {
		t.Fatalf("RunString: %v", err)
	}
	got := goaResult(t, bridge, "__res").String()
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

// TestHTTPFetchJS_WithNetworkPermission proves the gate opens with the
// permission declared: the fetch reaches the hook and returns the body.
func TestHTTPFetchJS_WithNetworkPermission(t *testing.T) {
	restore := setHTTPDo(func(b *HTTPBridge, req HTTPRequest) HTTPResponse {
		return HTTPResponse{Status: 200, Body: `{"plan":"pro"}`, Headers: map[string]string{}}
	})
	defer restore()

	ctx := newExtendedContext(t, t.TempDir(), NewHTTPBridge())
	bridge := NewJSBridge(PluginDef{ID: "test", Entry: "plugin.js", Permissions: []string{"network"}}, ctx)
	unlock := lockVM()
	_, err := bridge.vm.RunString(`goa.__res = JSON.stringify(goa.http.fetch("https://example.com/quota"))`)
	unlock()
	if err != nil {
		t.Fatalf("RunString: %v", err)
	}
	got := goaResult(t, bridge, "__res").String()
	if !strings.Contains(got, "plan") || !strings.Contains(got, "pro") {
		t.Fatalf("result = %s, want fetched body", got)
	}
}

// TestNetworkPermission_KnownName guards the manifest vocabulary: "network"
// must be a recognized permission.
func TestNetworkPermission_KnownName(t *testing.T) {
	if !knownPermissions["network"] {
		t.Fatal(`"network" is not a known permission; add it to knownPermissions`)
	}
}
