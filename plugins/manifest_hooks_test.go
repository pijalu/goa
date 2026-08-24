// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidatePluginDef_HooksAndPermissions is the §7 step 1 parse/validate
// table: every malformed shape must refuse the manifest, every legal shape
// must pass.
func TestValidatePluginDef_HooksAndPermissions(t *testing.T) {
	validHook := PluginHookDecl{Point: "tool-call:pre", Mode: "intercept", Description: "guard"}
	tests := []struct {
		name    string
		mutate  func(d *PluginDef)
		wantErr string // empty ⇒ expect success
	}{
		{"bare manifest", func(*PluginDef) {}, ""},
		{"valid single hook", func(d *PluginDef) { d.Hooks = []PluginHookDecl{validHook} }, ""},
		{"valid notify hook", func(d *PluginDef) {
			d.Hooks = []PluginHookDecl{{Point: "tool-call:post", Mode: "notify"}}
		}, ""},
		{"unknown point", func(d *PluginDef) {
			d.Hooks = []PluginHookDecl{{Point: "nope:not-a-point", Mode: "notify"}}
		}, `hook #1: unknown point "nope:not-a-point"`},
		{"empty point", func(d *PluginDef) {
			d.Hooks = []PluginHookDecl{{Point: "", Mode: "notify"}}
		}, `unknown point ""`},
		{"invalid mode", func(d *PluginDef) {
			d.Hooks = []PluginHookDecl{{Point: "tool-call:pre", Mode: "block"}}
		}, `mode must be "notify" or "intercept", got "block"`},
		{"duplicate mode+point", func(d *PluginDef) {
			d.Hooks = []PluginHookDecl{validHook, {Point: "tool-call:pre", Mode: "intercept"}}
		}, `duplicate hook declaration "intercept" at point "tool-call:pre"`},
		{"same point both modes is fine", func(d *PluginDef) {
			d.Hooks = []PluginHookDecl{
				{Point: "tool-call:pre", Mode: "intercept"},
				{Point: "tool-call:pre", Mode: "notify"},
			}
		}, ""},
		{"known permission", func(d *PluginDef) { d.Permissions = []string{"provider-keys"} }, ""},
		{"all known permissions", func(d *PluginDef) {
			d.Permissions = []string{"provider-keys", "ui-confirm", "account-write"}
		}, ""},
		{"unknown permission", func(d *PluginDef) { d.Permissions = []string{"root"} }, `unknown permission "root"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := &PluginDef{ID: "p", Name: "P"}
			tt.mutate(def)
			err := validatePluginDef(def)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected valid, got error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

// TestLoadManifest_HooksYAML proves the YAML schema round-trips hooks and
// permissions, and that malformed documents refuse to load.
func TestLoadManifest_HooksYAML(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
		check   func(t *testing.T, def *PluginDef) // only on success
	}{
		{
			name: "full schema",
			yaml: `
id: m6
name: M6 Plugin
version: 1.2.3
permissions: [provider-keys]
hooks:
  - point: tool-call:pre
    mode: intercept
    description: Redact AWS keys
  - point: tool-call:post
    mode: notify
`,
			check: func(t *testing.T, def *PluginDef) {
				if len(def.Hooks) != 2 {
					t.Fatalf("want 2 hooks, got %d", len(def.Hooks))
				}
				h := def.Hooks[0]
				if h.Point != "tool-call:pre" || h.Mode != "intercept" || h.Description != "Redact AWS keys" {
					t.Errorf("hook[0] mismatch: %+v", h)
				}
				if def.Hooks[1].Mode != "notify" {
					t.Errorf("hook[1] mode: %q", def.Hooks[1].Mode)
				}
				if len(def.Permissions) != 1 || def.Permissions[0] != "provider-keys" {
					t.Errorf("permissions: %v", def.Permissions)
				}
			},
		},
		{name: "unknown hook point", yaml: "id: m6\nname: P\nhooks:\n  - point: bogus\n    mode: notify\n", wantErr: "unknown point"},
		{name: "bad mode", yaml: "id: m6\nname: P\nhooks:\n  - point: tool-call:post\n    mode: deny\n", wantErr: "mode must be"},
		{name: "duplicate declaration", yaml: "id: m6\nname: P\nhooks:\n  - point: tool-call:post\n    mode: notify\n  - point: tool-call:post\n    mode: notify\n", wantErr: "duplicate"},
		{name: "unknown permission", yaml: "id: m6\nname: P\npermissions: [sudo]\n", wantErr: "unknown permission"},
		{name: "still validates base fields", yaml: "name: No ID\nhooks:\n  - point: tool-call:post\n    mode: notify\n", wantErr: "missing id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "plugin.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			def, err := loadManifest(path)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got: %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("loadManifest: %v", err)
			}
			if tt.check != nil {
				tt.check(t, def)
			}
		})
	}
}

// TestValidateManifest_HooksSurface mirrors install-time validation: the
// exported entry point must surface the same refusals.
func TestValidateManifest_HooksSurface(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugin.yaml")
	yaml := "id: bad\nname: Bad\nhooks:\n  - point: nope\n    mode: intercept\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	err := ValidateManifest(path)
	if err == nil || !strings.Contains(err.Error(), "unknown point") {
		t.Fatalf("ValidateManifest must refuse unknown points, got: %v", err)
	}
}

// TestLoaderRefusesMalformedManifest proves the discovery path refuses a
// plugin whose manifest fails validation and surfaces the reason in the log
// (config-error visibility consistent with existing manifest failures).
func TestLoaderRefusesMalformedManifest(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "bad-plugin")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "id: bad-plugin\nname: Bad\nhooks:\n  - point: wrong\n    mode: notify\n"
	if err := os.WriteFile(filepath.Join(bad, "plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	flags := log.Flags()
	prevOut, prevFlags := log.Writer(), flags
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer log.SetOutput(prevOut)
	defer log.SetFlags(prevFlags)

	pl := NewPluginLoader([]string{root}, []string{"*"})
	bridges, err := pl.LoadAll(PluginContext{})
	if err != nil {
		t.Fatalf("scan must survive refusal, got error: %v", err)
	}
	if len(bridges) != 0 {
		t.Fatalf("malformed plugin must be refused, loaded %d bridges", len(bridges))
	}
	if !strings.Contains(buf.String(), "refusing plugin") || !strings.Contains(buf.String(), "unknown point") {
		t.Fatalf("refusal reason must reach the log, got: %q", buf.String())
	}
}
