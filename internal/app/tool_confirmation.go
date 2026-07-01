// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pijalu/goa/internal/perms"
	"github.com/pijalu/goa/tui"
)

// wireToolConfirmation installs the tool-approval callback on the agent manager
// when running in an interactive TUI. The callback evaluates the path policy
// and, when required, shows a modal selector with once/always options.
func (a *App) wireToolConfirmation(engine *tui.TUI) {
	if a.subs == nil || a.subs.agentMgr == nil {
		return
	}
	a.subs.agentMgr.SetConfirmTool(func(ctx context.Context, toolName, input string) (bool, error) {
		return a.confirmTool(ctx, engine, toolName, input)
	})
}

// confirmTool asks the user to approve a tool call. It supports both
// session-scoped (once) and persisted (always) approvals/denials.
func (a *App) confirmTool(ctx context.Context, engine *tui.TUI, toolName, input string) (bool, error) {
	autonomy := a.subs.agentMgr.CurrentMode().Autonomy
	policy := perms.PathPolicy{ProjectDir: a.subs.projectDir, Autonomy: string(autonomy)}
	if policy.Decide(toolName, input) != perms.PathAsk {
		return true, nil
	}

	key := approvalKey(toolName, input, a.subs.projectDir)

	// Check persisted/session approvals.
	if a.isPathApproved(key) {
		return true, nil
	}
	if a.isPathDenied(key) {
		return false, fmt.Errorf("tool %q rejected by policy", toolName)
	}

	// Ask the user.
	choice, err := a.showToolApprovalDialog(ctx, engine, toolName, input)
	if err != nil {
		return false, err
	}
	switch choice {
	case "authorize-once":
		a.recordPathApproval(key, false)
		return true, nil
	case "authorize-always":
		a.recordPathApproval(key, true)
		return true, nil
	case "reject-once":
		return false, fmt.Errorf("tool %q was rejected", toolName)
	case "reject-always":
		a.recordPathDenial(key, true)
		return false, fmt.Errorf("tool %q was rejected permanently", toolName)
	default:
		return false, fmt.Errorf("tool %q was rejected", toolName)
	}
}

// approvalKey returns a stable key for a tool/path combination. The project
// directory is used to normalize relative paths so approvals survive across
// sessions when saved.
func approvalKey(toolName, input, projectDir string) string {
	paths := extractApprovalPaths(toolName, input)
	if len(paths) == 0 {
		return toolName + ":*"
	}
	parts := make([]string, 0, len(paths))
	for _, p := range paths {
		if !filepath.IsAbs(p) && projectDir != "" {
			p = filepath.Join(projectDir, p)
		}
		parts = append(parts, filepath.Clean(p))
	}
	return toolName + ":" + strings.Join(parts, ",")
}

func extractApprovalPaths(toolName, input string) []string {
	switch toolName {
	case "read", "write", "edit", "read_media_file":
		return permsExtractJSONPaths(input, "path", "file_path", "target_path")
	case "bash":
		return permsExtractBashPaths(input)
	}
	return nil
}

func permsExtractJSONPaths(input string, keys ...string) []string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(input), &payload); err != nil {
		return nil
	}
	keySet := make(map[string]bool, len(keys))
	for _, k := range keys {
		keySet[k] = true
	}
	var paths []string
	for k, v := range payload {
		if !keySet[k] {
			continue
		}
		if s, ok := v.(string); ok && s != "" {
			paths = append(paths, s)
		}
	}
	return paths
}

func permsExtractBashPaths(input string) []string {
	var payload struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(input), &payload); err != nil {
		return nil
	}
	cmd := strings.TrimSpace(payload.Command)
	if cmd == "" {
		return nil
	}
	var paths []string
	for _, tok := range strings.Fields(cmd) {
		if looksLikeApprovalPath(tok) {
			paths = append(paths, tok)
		}
	}
	return paths
}

func looksLikeApprovalPath(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") {
		return true
	}
	return false
}

func (a *App) isPathApproved(key string) bool {
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	return a.pathApprovals[key]
}

func (a *App) isPathDenied(key string) bool {
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	return a.pathDenials[key]
}

func (a *App) recordPathApproval(key string, persist bool) {
	a.statsMu.Lock()
	if a.pathApprovals == nil {
		a.pathApprovals = make(map[string]bool)
	}
	a.pathApprovals[key] = true
	a.statsMu.Unlock()
	if persist {
		a.persistPathApproval(key, true)
	}
}

func (a *App) recordPathDenial(key string, persist bool) {
	a.statsMu.Lock()
	if a.pathDenials == nil {
		a.pathDenials = make(map[string]bool)
	}
	a.pathDenials[key] = true
	a.statsMu.Unlock()
	if persist {
		a.persistPathApproval(key, false)
	}
}

func (a *App) persistPathApproval(key string, approved bool) {
	if a.subs == nil || a.subs.stateStore == nil {
		return
	}
	snap, err := a.subs.stateStore.Load()
	if err != nil {
		return
	}
	if approved {
		snap.ApprovedPaths = appendUnique(snap.ApprovedPaths, key)
		snap.DeniedPaths = removeString(snap.DeniedPaths, key)
	} else {
		snap.DeniedPaths = appendUnique(snap.DeniedPaths, key)
		snap.ApprovedPaths = removeString(snap.ApprovedPaths, key)
	}
	_ = a.subs.stateStore.Save(snap)
}

func (a *App) loadPersistedPathApprovals() {
	if a.subs == nil || a.subs.stateStore == nil {
		return
	}
	snap, err := a.subs.stateStore.Load()
	if err != nil {
		return
	}
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	if a.pathApprovals == nil {
		a.pathApprovals = make(map[string]bool)
	}
	if a.pathDenials == nil {
		a.pathDenials = make(map[string]bool)
	}
	for _, k := range snap.ApprovedPaths {
		a.pathApprovals[k] = true
	}
	for _, k := range snap.DeniedPaths {
		a.pathDenials[k] = true
	}
}

func (a *App) showToolApprovalDialog(ctx context.Context, engine *tui.TUI, toolName, input string) (string, error) {
	if engine == nil {
		return "", fmt.Errorf("no TUI available for tool confirmation")
	}
	title := fmt.Sprintf("Approve %s?", toolName)
	items := []tui.SelectorItem{
		{Value: "authorize-once", Label: "Authorize once", Description: formatApprovalMessage(toolName, input)},
		{Value: "authorize-always", Label: "Authorize always", Description: "Save approval for this path"},
		{Value: "reject-once", Label: "Reject", Description: "Deny this call only"},
		{Value: "reject-always", Label: "Reject always", Description: "Save rejection for this path"},
	}
	ch := engine.ShowSelector(title, items, "")
	select {
	case choice := <-ch:
		return choice, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func formatApprovalMessage(toolName, input string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(input), &payload); err == nil {
		oneLine := strings.Join(strings.Fields(string(mustJSON(payload))), " ")
		if len(oneLine) > 80 {
			oneLine = oneLine[:77] + "..."
		}
		return oneLine
	}
	oneLine := strings.Join(strings.Fields(input), " ")
	if len(oneLine) > 80 {
		oneLine = oneLine[:77] + "..."
	}
	return oneLine
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func appendUnique(slice []string, s string) []string {
	for _, v := range slice {
		if v == s {
			return slice
		}
	}
	return append(slice, s)
}

func removeString(slice []string, s string) []string {
	out := slice[:0]
	for _, v := range slice {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

// approvalStateFields are added to App to track session-scoped approvals.
type approvalStateFields struct {
	pathApprovals map[string]bool
	pathDenials   map[string]bool
}
