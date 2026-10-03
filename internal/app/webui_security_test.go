// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

// The CLI seam for the web UI's security flags: the address default, the
// mode parsing, and the rule that an unauthenticated non-loopback bind is
// refused before the listener exists.

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/webui"
)

// The default address has to be loopback: it is what makes `--server-auth=none`
// a safe default rather than a footgun.
func TestServerAddr_DefaultsToLoopback(t *testing.T) {
	got := serverAddr(RuntimeOptions{})
	if got != webui.DefaultAddr {
		t.Fatalf("default addr = %q, want %q", got, webui.DefaultAddr)
	}
	if !webui.IsLoopbackAddr(got) {
		t.Fatalf("default addr %q is not loopback", got)
	}
	if got := serverAddr(RuntimeOptions{ServerAddr: "0.0.0.0:9000"}); got != "0.0.0.0:9000" {
		t.Fatalf("explicit addr = %q, want 0.0.0.0:9000", got)
	}
}

func TestWebuiAuthConfig_ParseModes(t *testing.T) {
	tests := []struct {
		name    string
		opts    RuntimeOptions
		want    webui.AuthMode
		wantErr bool
	}{
		{name: "empty means none", opts: RuntimeOptions{}, want: webui.AuthNone},
		{name: "basic", opts: RuntimeOptions{ServerAuth: "basic", ServerAuthUser: "op", ServerAuthPass: "pw"}, want: webui.AuthBasic},
		{name: "token", opts: RuntimeOptions{ServerAuth: "token", ServerAuthToken: "t"}, want: webui.AuthToken},
		{name: "case-insensitive", opts: RuntimeOptions{ServerAuth: "TOKEN", ServerAuthToken: "t"}, want: webui.AuthToken},
		{name: "typo is an error", opts: RuntimeOptions{ServerAuth: "basci"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := webuiAuthConfig(tc.opts)
			if (err != nil) != tc.wantErr {
				t.Fatalf("webuiAuthConfig err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if cfg.Mode != tc.want {
				t.Fatalf("mode = %q, want %q", cfg.Mode, tc.want)
			}
			if cfg.Username != tc.opts.ServerAuthUser || cfg.Token != tc.opts.ServerAuthToken {
				t.Errorf("credentials not carried: %+v", cfg)
			}
		})
	}
}

// The whole point of the guard: the flags the CLI collects must be enough for
// webui.CheckExposure to make the same decision the server would.
func TestWebuiAuthConfig_SatisfiesTheExposureRule(t *testing.T) {
	cases := []struct {
		name    string
		opts    RuntimeOptions
		wantErr bool
	}{
		{
			name: "default is safe",
			opts: RuntimeOptions{},
		},
		{
			name:    "off-loopback without auth is refused",
			opts:    RuntimeOptions{ServerAddr: "0.0.0.0:8080"},
			wantErr: true,
		},
		{
			name:    "off-loopback with token is allowed",
			opts:    RuntimeOptions{ServerAddr: "0.0.0.0:8080", ServerAuth: "token", ServerAuthToken: "t"},
			wantErr: false,
		},
		{
			name:    "off-loopback with basic is allowed",
			opts:    RuntimeOptions{ServerAddr: "192.168.0.9:8080", ServerAuth: "basic", ServerAuthUser: "op", ServerAuthPass: "pw"},
			wantErr: false,
		},
		{
			name:    "the explicit opt-in unblocks it",
			opts:    RuntimeOptions{ServerAddr: "0.0.0.0:8080", InsecureNoAuth: true},
			wantErr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := webuiAuthConfig(tc.opts)
			if err != nil {
				t.Fatalf("webuiAuthConfig: %v", err)
			}
			err = webui.CheckExposure(serverAddr(tc.opts), cfg, tc.opts.InsecureNoAuth)
			if (err != nil) != tc.wantErr {
				t.Fatalf("CheckExposure err = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "--insecure-no-auth") {
				t.Errorf("the refusal must name the way out: %v", err)
			}
		})
	}
}

// Basic auth without a password is a configuration error, not a silent open
// door: the mode parse succeeds but the authenticator refuses to be built.
func TestWebuiAuthConfig_MissingCredentialsAreRefused(t *testing.T) {
	cfg, err := webuiAuthConfig(RuntimeOptions{ServerAuth: "basic"})
	if err != nil {
		t.Fatalf("webuiAuthConfig: %v", err)
	}
	if _, err := webui.NewAuthenticator(cfg); err == nil {
		t.Error("basic auth without a password was accepted")
	}
	cfg, err = webuiAuthConfig(RuntimeOptions{ServerAuth: "token"})
	if err != nil {
		t.Fatalf("webuiAuthConfig: %v", err)
	}
	if _, err := webui.NewAuthenticator(cfg); err == nil {
		t.Error("token auth without a token was accepted")
	}
}

// The secret must not have to appear on the command line.
func TestServerAuthSecret_PrefersTheEnvironment(t *testing.T) {
	t.Setenv("GOA_SERVER_AUTH_TOKEN", "from-env")
	if got := serverAuthSecret("from-flag", "GOA_SERVER_AUTH_TOKEN"); got != "from-env" {
		t.Fatalf("serverAuthSecret = %q, want the environment value", got)
	}
	t.Setenv("GOA_SERVER_AUTH_PASSWORD", "")
	if got := serverAuthSecret("from-flag", "GOA_SERVER_AUTH_PASSWORD"); got != "from-flag" {
		t.Fatalf("serverAuthSecret = %q, want the flag fallback", got)
	}
}

// The usage text is the operator's only chance to learn the rules before
// typing the flags, so it has to state them.
// The refusal must happen before anything is bound: `goa server
// --server-addr 0.0.0.0:8080` has to stop with a message, not start an open
// remote control on the LAN.
func TestNewWebServer_RefusesOffLoopbackWithoutAuth(t *testing.T) {
	subs := &subsystems{}
	srv, err := newWebServer(subs, RuntimeOptions{ServerAddr: "0.0.0.0:8080"})
	if err == nil {
		_ = srv.Close()
		t.Fatal("an unauthenticated 0.0.0.0 bind was allowed")
	}
	if !strings.Contains(err.Error(), "--insecure-no-auth") {
		t.Errorf("the refusal must name the way out: %v", err)
	}
	if srv != nil {
		t.Error("a refused configuration must not produce a server")
	}
	if subs.terminal != nil {
		t.Error("a refused configuration must not even inject a terminal")
	}
}

func TestNewWebServer_AcceptsTheSafeConfigurations(t *testing.T) {
	cases := []struct {
		name string
		opts RuntimeOptions
	}{
		{name: "default loopback", opts: RuntimeOptions{}},
		{name: "token auth off-loopback", opts: RuntimeOptions{ServerAddr: "0.0.0.0:8080", ServerAuth: "token", ServerAuthToken: "t"}},
		{name: "basic auth off-loopback", opts: RuntimeOptions{ServerAddr: "0.0.0.0:8080", ServerAuth: "basic", ServerAuthUser: "op", ServerAuthPass: "pw"}},
		{name: "explicitly insecure", opts: RuntimeOptions{ServerAddr: "0.0.0.0:8080", InsecureNoAuth: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subs := &subsystems{}
			srv, err := newWebServer(subs, tc.opts)
			if err != nil {
				t.Fatalf("newWebServer: %v", err)
			}
			t.Cleanup(func() { _ = srv.Close() })
			if subs.terminal == nil {
				t.Error("the virtual terminal was not injected")
			}
		})
	}
}

func TestNewWebServer_RejectsAnUnknownAuthMode(t *testing.T) {
	subs := &subsystems{}
	if _, err := newWebServer(subs, RuntimeOptions{ServerAuth: "kerberos"}); err == nil {
		t.Fatal("an unknown auth mode was accepted")
	}
}

func TestWebServerUsage_DocumentsTheSecurityFlags(t *testing.T) {
	for _, want := range []string{
		"--server-auth", "--insecure-no-auth", "GOA_SERVER_AUTH_TOKEN",
		"loopback", "HttpOnly", "Content-Security-Policy",
	} {
		if !strings.Contains(webServerUsage, want) {
			t.Errorf("usage text does not mention %q", want)
		}
	}
}
