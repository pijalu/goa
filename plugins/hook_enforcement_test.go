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

// enforceFixtureDef is the manifest used across enforcement tests: one
// intercept + one notify declaration.
func enforceFixtureDef() *PluginDef {
	return &PluginDef{
		ID:      "ext",
		Name:    "External",
		Version: "1.0.0",
		Hooks: []PluginHookDecl{
			{Point: "tool-call:pre", Mode: "intercept"},
			{Point: "tool-call:post", Mode: "notify"},
		},
	}
}

func newEnforceFixture(t *testing.T, headless bool) (*HookEnforcer, *GrantStore) {
	t.Helper()
	store := NewGrantStore(t.TempDir())
	enf := NewHookEnforcer(store, headless)
	enf.Declare(enforceFixtureDef(), false)
	return enf, store
}

func specFor(point string, mode HookMode) HookSpec {
	return HookSpec{PluginID: "ext", Name: "h-" + point, Point: point, Mode: mode}
}

func TestHookEnforcer_DeclarationGate(t *testing.T) {
	enf, store := newEnforceFixture(t, false)

	// Undeclared registration is refused outright — even with a fresh grant.
	if err := store.Approve("ext", NewPluginGrant(enf.DeclaredFor("ext"), []GrantHook{
		{Point: "message:pre-send", Mode: "intercept"},
	})); err != nil {
		t.Fatal(err)
	}
	err := enf.Allow(specFor("message:pre-send", HookIntercept))
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("undeclared registration must be refused regardless of grants, got: %v", err)
	}

	// Unknown plugin (no discovered manifest) likewise.
	enf2 := NewHookEnforcer(store, false)
	err = enforcerAllow(enf2, specFor("tool-call:post", HookNotify))
	if err == nil || !strings.Contains(err.Error(), "no discovered manifest") {
		t.Fatalf("unknown plugin must be refused, got: %v", err)
	}
}

// helper so the table tests below stay readable
func enforcerAllow(e *HookEnforcer, s HookSpec) error { return e.Allow(s) }

// applyHookDecision grants the given decision strings for def, failing the
// test on approval errors so table entries stay straight-line.
func applyHookDecision(t *testing.T, store *GrantStore, def *PluginDef, decisions ...string) {
	t.Helper()
	if err := ApplyHookDecision(store, def, decisions); err != nil {
		t.Fatal(err)
	}
}

// approveDefVersion approves def under an explicit version string.
func approveDefVersion(t *testing.T, store *GrantStore, def *PluginDef, version string) {
	t.Helper()
	d := *def
	d.Version = version
	if err := store.Approve(def.ID, NewPluginGrant(&d, nil)); err != nil {
		t.Fatal(err)
	}
}

func TestHookEnforcer_GrantGate(t *testing.T) {
	var tests = []grantGateCase{
		{name: "approved pair passes", grant: func(t *testing.T, def *PluginDef, store *GrantStore) {
			applyHookDecision(t, store, def, "intercept|tool-call:pre")
		}, point: "tool-call:pre", mode: HookIntercept},
		{name: "declared but unapproved pair fails", grant: func(t *testing.T, def *PluginDef, store *GrantStore) {
			applyHookDecision(t, store, def, "intercept|tool-call:pre")
		}, point: "tool-call:post", mode: HookNotify,
			wantErr: "not selected in the install review"},
		{name: "missing grant fails", grant: func(*testing.T, *PluginDef, *GrantStore) {},
			point: "tool-call:pre", mode: HookIntercept, wantErr: "(re-)approval required"},
		{name: "stale version re-prompts", grant: func(t *testing.T, def *PluginDef, store *GrantStore) {
			approveDefVersion(t, store, def, "0.9.0")
		}, point: "tool-call:pre", mode: HookIntercept, wantErr: "(re-)approval required"},
		{name: "stale fingerprint re-prompts", grant: func(t *testing.T, def *PluginDef, store *GrantStore) {
			old := &PluginDef{ID: def.ID, Version: def.Version, Hooks: def.Hooks[:1]}
			if err := store.Approve(def.ID, NewPluginGrant(old, nil)); err != nil {
				t.Fatal(err)
			}
		}, point: "tool-call:post", mode: HookNotify, wantErr: "(re-)approval required"},
		{name: "headless fail-closed despite grant", headless: true,
			grant: func(t *testing.T, def *PluginDef, store *GrantStore) {
				applyHookDecision(t, store, def, "intercept|tool-call:pre")
			}, point: "tool-call:pre", mode: HookIntercept,
			wantErr: HookEscapeEnv},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { runGrantGateCase(t, tt) })
	}
}

// grantGateCase mirrors the anonymous struct used in TestHookEnforcer_GrantGate.
type grantGateCase = struct {
	name     string
	grant    func(t *testing.T, def *PluginDef, store *GrantStore)
	point    string
	mode     HookMode
	wantErr  string // empty ⇒ allowed
	headless bool
}

// runGrantGateCase executes one grant-gate scenario.
func runGrantGateCase(t *testing.T, tt grantGateCase) {
	enf, store := newEnforceFixture(t, tt.headless)
	def := enforceFixtureDef()
	tt.grant(t, def, store)
	err := enforcerAllow(enf, specFor(tt.point, tt.mode))
	switch {
	case tt.wantErr == "":
		if err != nil {
			t.Fatalf("expected allow, got: %v", err)
		}
	default:
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Fatalf("expected error containing %q, got: %v", tt.wantErr, err)
		}
	}
}

// §7 step 4: bundled plugins ship pre-approved — no grants.json entry needed,
// but the manifest declaration gate still binds them.
func TestHookEnforcer_BundledExempt(t *testing.T) {
	enf, _ := newEnforceFixture(t, false)
	bundledDef := &PluginDef{ID: "provider-quota", Name: "Q", Version: "1",
		Hooks: []PluginHookDecl{{Point: "tool-call:post", Mode: "notify"}}}
	enf.Declare(bundledDef, true)
	spec := HookSpec{PluginID: "provider-quota", Name: "q", Point: "tool-call:post", Mode: HookNotify}
	if err := enforcerAllow(enf, spec); err != nil {
		t.Fatalf("bundled plugin must skip the grant flow, got: %v", err)
	}
	// …but the declaration gate still applies to bundled code.
	err := enforcerAllow(enf, HookSpec{PluginID: "provider-quota", Name: "q2", Point: "message:pre-send", Mode: HookIntercept})
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("bundled undeclared hook must still be refused, got: %v", err)
	}
}

// §7 step 5 escape hatch: GOA_PLUGIN_HOOKS_APPROVED=1 lets a granted external
// plugin register in headless mode; without it the gate refuses.
func TestHookEnforcer_HeadlessEscapeHatch(t *testing.T) {
	t.Setenv(HookEscapeEnv, "1")
	enf, store := newEnforceFixture(t, true)
	if err := ApplyHookDecision(store, enforceFixtureDef(), []string{"intercept|tool-call:pre"}); err != nil {
		t.Fatal(err)
	}
	if err := enforcerAllow(enf, specFor("tool-call:pre", HookIntercept)); err != nil {
		t.Fatalf("escape hatch must restore normal grant behavior headlessly, got: %v", err)
	}
}

func TestHookEnforcer_NeedsApprovalAndPending(t *testing.T) {
	enf, store := newEnforceFixture(t, false)
	enf.Declare(&PluginDef{ID: "bare", Name: "Bare", Version: "1"}, false)
	enf.Declare(&PluginDef{ID: "quota-bundled", Name: "Q", Version: "1",
		Hooks: []PluginHookDecl{{Point: "tool-call:post", Mode: "notify"}}}, true)

	// Nothing granted yet ⇒ ext needs approval; bare has nothing to review;
	// bundled never prompts.
	if !enf.NeedsApproval("ext") {
		t.Error("fresh external plugin with hooks needs approval")
	}
	if enf.NeedsApproval("bare") || enf.NeedsApproval("quota-bundled") || enf.NeedsApproval("ghost") {
		t.Error("no-review / bundled / unknown plugins must not prompt")
	}

	if err := ApplyHookDecision(store, enforceFixtureDef(), []string{"notify|tool-call:post"}); err != nil {
		t.Fatal(err)
	}
	if enf.NeedsApproval("ext") {
		t.Error("fresh grant satisfies review")
	}

	// Version drift flips it back on (the re-prompt trigger).
	drifted := enforceFixtureDef()
	drifted.Version = "2.0.0"
	enf.Declare(drifted, false)
	if !enf.NeedsApproval("ext") {
		t.Error("version drift must re-prompt")
	}
	pending := enforcerPending(enf)
	if len(pending) != 1 || pending[0] != "ext" {
		t.Fatalf("pending = %v, want [ext]", pending)
	}
}

func enforcerPending(e *HookEnforcer) []string { return e.PendingApprovals() }

func TestHookEnforcer_ObserveDir(t *testing.T) {
	root := t.TempDir()
	writePlug := func(name, yaml string) {
		t.Helper()
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writePlug("good", "id: good\nname: Good\nversion: 1\nhooks:\n  - point: tool-call:post\n    mode: notify\n")
	writePlug("bad", "id: bad\nname: Bad\nhooks:\n  - point: nope\n    mode: notify\n")

	enf := NewHookEnforcer(NewGrantStore(t.TempDir()), false)
	enf.ObserveDir(root, false)

	if enf.DeclaredFor("bad") != nil {
		t.Error("invalid manifest must not be declared")
	}
	good := enf.DeclaredFor("good")
	if good == nil || len(good.Hooks) != 1 {
		t.Fatalf("valid manifest must be declared, got %+v", good)
	}
	// DeclaredFor returns a copy — mutating it must not corrupt enforcement.
	good.Hooks[0].Point = "tampered"
	if again := enf.DeclaredFor("good"); again.Hooks[0].Point != "tool-call:post" {
		t.Fatal("DeclaredFor must hand out defensive copies")
	}
}

// M6 installs the real validator into the M2 slot via SetAllow; prove the
// registry consults the swapped-in enforcer.
func TestHookRegistry_SetAllowInstallsEnforcer(t *testing.T) {
	reg := NewHookRegistry(nil) // boot state: permissive
	if err := reg.Register(specFor("tool-call:post", HookNotify), func(map[string]any) map[string]any { return nil }); err != nil {
		t.Fatalf("nil validator allows registration: %v", err)
	}

	enf, store := newEnforceFixture(t, false)
	reg.SetAllow(enf.Allow) // M6 wiring point

	// The already-registered entry stays; a NEW ungranted registration is now
	// rejected through the same path the JS bridge drives.
	err := reg.Register(HookSpec{PluginID: "ext", Name: "late", Point: "tool-call:pre", Mode: HookIntercept},
		func(map[string]any) map[string]any { return nil })
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("SetAllow must activate enforcement, got: %v", err)
	}
	if reg.Count() != 1 {
		t.Fatalf("want exactly the pre-existing entry, have %d", reg.Count())
	}

	if err := ApplyHookDecision(store, enforceFixtureDef(), []string{"intercept|tool-call:pre"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(specFor("tool-call:pre", HookIntercept), func(map[string]any) map[string]any { return nil }); err != nil {
		t.Fatalf("granted registration must pass after SetAllow: %v", err)
	}
}
