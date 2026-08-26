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

func newTestGrantStore(t *testing.T) *GrantStore {
	t.Helper()
	return NewGrantStore(t.TempDir())
}

func TestGrantStore_RoundTripAndPreservesOthers(t *testing.T) {
	store := newTestGrantStore(t)
	defA := &PluginDef{ID: "a", Name: "A", Version: "1.0.0", Hooks: []PluginHookDecl{
		{Point: "tool-call:post", Mode: "notify"},
	}}
	defB := &PluginDef{ID: "b", Name: "B", Version: "2.0.0"}
	approveGrantFor(t, store, defA, []GrantHook{{Point: "tool-call:post", Mode: "notify"}})
	approveGrantFor(t, store, defB, nil)
	assertGrantA(t, store, defA)
	assertGrantBPreservesA(t, store)
}

// approveGrantFor approves the plugin's current identity with hooks.
func approveGrantFor(t *testing.T, store *GrantStore, def *PluginDef, hooks []GrantHook) {
	t.Helper()
	if err := store.Approve(def.ID, NewPluginGrant(def, hooks)); err != nil {
		t.Fatalf("Approve %s: %v", def.ID, err)
	}
}

// assertGrantA checks a's stored grant round-trips exactly.
func assertGrantA(t *testing.T, store *GrantStore, defA *PluginDef) {
	t.Helper()
	gA, okA, err := store.Get("a")
	if err != nil || !okA {
		t.Fatalf("Get a: ok=%v err=%v", okA, err)
	}
	if gA.Version != "1.0.0" || len(gA.ApprovedHooks) != 1 || gA.ApprovedHooks[0].Point != "tool-call:post" {
		t.Fatalf("grant a mismatch: %+v", gA)
	}
	if gA.ManifestHash != ManifestFingerprint(defA.Hooks) {
		t.Errorf("fingerprint not snapshotted")
	}
	if gA.ApprovedAt.IsZero() {
		t.Errorf("ApprovedAt must be stamped")
	}
}

// assertGrantBPreservesA checks b's grant exists intact and did not clobber a.
func assertGrantBPreservesA(t *testing.T, store *GrantStore) {
	t.Helper()
	gB, okB, _ := store.Get("b")
	if !okB || gB.Version != "2.0.0" || len(gB.ApprovedHooks) != 0 {
		t.Fatalf("grant b mismatch: ok=%v %+v", okB, gB)
	}
	if _, ok, _ := store.Get("a"); !ok {
		t.Fatal("grant a vanished after approving b")
	}
}

// §7 step 2: grants.json carries security decisions → 0600 like StorageBridge.
func TestGrantStore_FilePerms0600(t *testing.T) {
	dir := t.TempDir()
	store := NewGrantStore(dir)
	if err := store.Approve("p", PluginGrant{Version: "1"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "grants.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("grants.json perms = %o, want 600", got)
	}
}

func TestGrantStore_MissingFileReadsAsNoGrant(t *testing.T) {
	store := newTestGrantStore(t)
	if _, ok, err := store.Get("ghost"); ok || err != nil {
		t.Fatalf("absent grant must read as (false, nil), got ok=%v err=%v", ok, err)
	}
	if err := store.Revoke("ghost"); err != nil {
		t.Fatalf("revoke of missing id must be a no-op, got %v", err)
	}
}

// Fail-closed policy: corrupt state never reads as consent; writes refuse to
// clobber unreadable files; revoke is allowed to reset them.
func TestGrantStore_CorruptFailsClosed(t *testing.T) {
	dir := t.TempDir()
	store := NewGrantStore(dir)
	path := filepath.Join(dir, "grants.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Get("p"); ok || err == nil {
		t.Fatalf("corrupt file must yield no-grant + error, got ok=%v err=%v", ok, err)
	}
	if err := store.Approve("p", PluginGrant{Version: "1"}); err == nil {
		t.Fatal("Approve must refuse to overwrite an unreadable file")
	}
	if err := store.Revoke("p"); err != nil {
		t.Fatalf("Revoke resets corrupt state: %v", err)
	}
	if _, _, err := store.Get("p"); err != nil {
		t.Fatalf("file should be usable again after revoke-reset: %v", err)
	}
}

func TestManifestFingerprint_OrderInsensitive_ContentSensitive(t *testing.T) {
	set1 := []PluginHookDecl{
		{Point: "tool-call:pre", Mode: "intercept"},
		{Point: "tool-call:post", Mode: "notify"},
	}
	reordered := []PluginHookDecl{set1[1], set1[0]}

	if ManifestFingerprint(set1) != ManifestFingerprint(reordered) {
		t.Error("reordering the YAML list must not change the fingerprint")
	}
	added := append(append([]PluginHookDecl{}, set1...), PluginHookDecl{Point: "message:pre-send", Mode: "intercept"})
	if ManifestFingerprint(set1) == ManifestFingerprint(added) {
		t.Error("adding a declared hook must change the fingerprint")
	}
	descOnly := []PluginHookDecl{
		{Point: "tool-call:pre", Mode: "intercept", Description: "now documented"},
		{Point: "tool-call:post", Mode: "notify"},
	}
	if ManifestFingerprint(set1) != ManifestFingerprint(descOnly) {
		t.Error("description-only edits are cosmetic and must not invalidate grants")
	}
	if ManifestFingerprint(nil) == "" {
		t.Error("empty list still needs a stable hash")
	}
}

// §7 step 2 staleness rule: missing grant OR version drift OR fingerprint
// drift ⇒ re-prompt; identical or reorder-only manifests stay fresh.
func TestGrantStale_Table(t *testing.T) {
	hooks := []PluginHookDecl{
		{Point: "tool-call:pre", Mode: "intercept"},
		{Point: "tool-call:post", Mode: "notify"},
	}
	def := &PluginDef{ID: "p", Name: "P", Version: "1.0.0", Hooks: hooks}
	fresh := NewPluginGrant(def, nil)

	tests := []struct {
		name    string
		grant   PluginGrant
		exists  bool
		current *PluginDef
		want    bool
	}{
		{"missing grant", PluginGrant{}, false, def, true},
		{"identical manifest", fresh, true, def, false},
		{"version bump", func() PluginGrant { g := fresh; g.Version = "1.1.0"; return g }(), true, def, true},
		{"manifest adds a hook", fresh, true, &PluginDef{ID: "p", Version: "1.0.0", Hooks: append(append([]PluginHookDecl{}, hooks...), PluginHookDecl{Point: "message:pre-send", Mode: "intercept"})}, true},
		{"manifest drops a hook", fresh, true, &PluginDef{ID: "p", Version: "1.0.0", Hooks: hooks[:1]}, true},
		{"reorder only", fresh, true, &PluginDef{ID: "p", Version: "1.0.0", Hooks: []PluginHookDecl{hooks[1], hooks[0]}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GrantStale(tt.grant, tt.exists, tt.current); got != tt.want {
				t.Fatalf("GrantStale = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApplyHookDecision_MapsSelectedRowsToPairs(t *testing.T) {
	def := &PluginDef{ID: "m6", Name: "M6", Version: "3.1.0", Hooks: []PluginHookDecl{
		{Point: "tool-call:pre", Mode: "intercept", Description: "guard"},
		{Point: "tool-call:post", Mode: "notify"},
	}}
	store := newTestGrantStore(t)

	// Accept only the notify row (conservative partial acceptance).
	err := ApplyHookDecision(store, def, []string{ReviewRowID(def.Hooks[1])})
	if err != nil {
		t.Fatalf("ApplyHookDecision: %v", err)
	}
	g, ok, _ := store.Get("m6")
	if !ok || len(g.ApprovedHooks) != 1 ||
		g.ApprovedHooks[0].Mode != "notify" || g.ApprovedHooks[0].Point != "tool-call:post" {
		t.Fatalf("approved pair mapping wrong: ok=%v %+v", ok, g)
	}
	if GrantStale(g, true, def) {
		t.Fatal("freshly written decision must count as current")
	}

	// Accepting nothing stores an empty (deny-all-hooks) but valid grant.
	if err := ApplyHookDecision(store, def, nil); err != nil {
		t.Fatalf("zero-selection accept: %v", err)
	}
	g, _, _ = store.Get("m6")
	if len(g.ApprovedHooks) != 0 {
		t.Fatalf("want empty approved set, got %+v", g.ApprovedHooks)
	}
}

func TestReviewRowID_BuildHookReview(t *testing.T) {
	def := &PluginDef{ID: "m6", Name: "Reviewer", Version: "9.9.9",
		Hooks: []PluginHookDecl{
			{Point: "tool-call:pre", Mode: "intercept"},
			{Point: "tool-call:post", Mode: "notify", Description: "say hi"},
		},
		Permissions: []string{"ui-confirm"},
	}
	review := BuildHookReview(def)
	if review == nil {
		t.Fatal("hooks+permissions require review content")
	}
	if review.PluginID != "m6" || review.Title == "" || review.Body == "" {
		t.Fatalf("review header incomplete: %+v", review)
	}
	if len(review.Rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(review.Rows))
	}
	// Conservative defaults (§7 step 3): notify pre-checked, intercept off.
	if review.Rows[0].DefaultOn {
		t.Error("intercept rows default OFF")
	}
	if !review.Rows[1].DefaultOn {
		t.Error("notify rows default ON")
	}
	for i, h := range def.Hooks {
		if review.Rows[i].ID != ReviewRowID(h) {
			t.Errorf("row[%d].ID = %q, want %q", i, review.Rows[i].ID, ReviewRowID(h))
		}
	}
	if !strings.Contains(review.Body, "ui-confirm") {
		t.Error("permission summary missing from body")
	}

	bare := BuildHookReview(&PluginDef{ID: "bare", Name: "Bare"})
	if bare != nil {
		t.Fatal("hook-less, permission-less plugin needs no review card")
	}
}
