// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import "fmt"

// HookReviewRow is one toggleable row of the install-time review card
// (§7 step 3). ID is the opaque selection key (mode|point) echoed back by the
// UI; DefaultOn implements the conservative defaults (notify pre-checked,
// intercept off until explicitly enabled).
type HookReviewRow struct {
	ID        string
	Label     string
	DefaultOn bool
}

// HookReview is everything the host UI needs to render ONE install-review
// card for a plugin. Plain data — no TUI types — so any frontend (TUI,
// headless renderer, tests) can consume it.
type HookReview struct {
	PluginID string
	Title    string
	Body     string
	Rows     []HookReviewRow
}

// ReviewRowID builds the stable row id for one manifest declaration.
func ReviewRowID(h PluginHookDecl) string { return h.Mode + "|" + h.Point }

// BuildHookReview assembles the §7 step 3 review card content for def:
// plugin name/version header, per-hook rows "[mode] point — description" and
// the permission summary. Returns nil when nothing requires review.
func BuildHookReview(def *PluginDef) *HookReview {
	if !RequiresReview(def) {
		return nil
	}
	version := def.Version
	if version == "" {
		version = "unknown version"
	}
	review := &HookReview{
		PluginID: def.ID,
		Title:    fmt.Sprintf("Plugin %s v%s", def.Name, version),
		Body: "This plugin wants to integrate with the agent loop. " +
			"Intercept hooks can rewrite or block tool calls and messages; " +
			"toggle only what you trust.",
	}
	for _, h := range def.Hooks {
		desc := h.Description
		if desc == "" {
			desc = "(no description)"
		}
		review.Rows = append(review.Rows, HookReviewRow{
			ID:        ReviewRowID(h),
			Label:     fmt.Sprintf("[%s] %s — %s", h.Mode, h.Point, desc),
			DefaultOn: HookMode(h.Mode) == HookNotify,
		})
	}
	if len(def.Permissions) > 0 {
		review.Body += "\nPermissions requested: " + joinStrings(def.Permissions, ", ")
	}
	return review
}

// ApplyHookDecision persists a review outcome: selected row ids are mapped
// back to their (mode, point) declarations and stored as the plugin's grant.
// Accepting with zero rows selected stores an empty grant — the plugin is
// approved but none of its hooks may register.
func ApplyHookDecision(store *GrantStore, def *PluginDef, selectedIDs []string) error {
	selected := make(map[string]bool, len(selectedIDs))
	for _, id := range selectedIDs {
		selected[id] = true
	}
	approved := make([]GrantHook, 0, len(def.Hooks))
	for _, h := range def.Hooks {
		if selected[ReviewRowID(h)] {
			approved = append(approved, GrantHook{Point: h.Point, Mode: h.Mode})
		}
	}
	return store.Approve(def.ID, NewPluginGrant(def, approved))
}

// joinStrings is strings.Join without importing the package twice in files
// that already alias it differently — trivial readability helper.
func joinStrings(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}
