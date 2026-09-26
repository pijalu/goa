// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
)

// ToolToggleOutcome reports what applying a tool enable/disable did at runtime.
// Every surface (/config → Tools, /tools:<name>:on|off, /docs …:on|off) renders
// the SAME outcome wording, so the surfaces can never disagree.
type ToolToggleOutcome int

const (
	// ToolToggleUnchanged: the config flag already had the requested value.
	ToolToggleUnchanged ToolToggleOutcome = iota
	// ToolToggleApplied: the flag was flipped and the live registry + pushed
	// agent tool set were updated — no restart needed.
	ToolToggleApplied
	// ToolToggleRestartRequired: the flag was flipped but the tool could not be
	// built at runtime; the next process start will register it.
	ToolToggleRestartRequired
)

// ToolRestartMessage is the single wording for "this tool needs a restart".
// It is exported so the host (internal/app) can assert the contract in tests
// and so no surface can invent a different (or missing) message.
func ToolRestartMessage(name string) string {
	return fmt.Sprintf("Tool %s could not be instantiated at runtime. Restart Goa to apply the change.", name)
}

// ToolToggleMessage returns the user-facing outcome line for a toggle.
func ToolToggleMessage(name string, enabled bool, outcome ToolToggleOutcome) string {
	switch outcome {
	case ToolToggleUnchanged:
		return fmt.Sprintf("Tool %s is already %s.", name, onOffLabel(enabled))
	case ToolToggleRestartRequired:
		return ToolRestartMessage(name)
	default:
		return fmt.Sprintf("Tool %s %s. %s", name, onOffLabel(enabled), restartHint(enabled))
	}
}

// toolSaveError is a failed persistence of a tool's enabled flag. Its Error()
// text stays lowercase (ST1005); ToolToggleErrorText renders the user-facing
// wording, which is the long-standing config-surface one.
type toolSaveError struct{ err error }

func (e *toolSaveError) Error() string { return "failed to save config: " + e.err.Error() }
func (e *toolSaveError) Unwrap() error { return e.err }

// ToolToggleErrorText renders a toggle failure for the user, keeping the
// "Failed to save config: …" wording every config surface has always used.
func ToolToggleErrorText(err error) string {
	var se *toolSaveError
	if errors.As(err, &se) {
		return "Failed to save config: " + se.err.Error()
	}
	return err.Error()
}

// errToolToggleNoConfig is returned when a toggle is attempted without a loaded
// configuration; the caller surfaces it the way the command surface always has
// (as an error), while runtime failures are rendered as output.
var errToolToggleNoConfig = errors.New("configuration not available")

// ApplyToolToggle is THE enable/disable primitive: it flips the config flag,
// persists it, and applies the change to the live registry and the running
// agent's tool set. /config → Tools and /tools:<name>:on|off both call it, so
// they can never drift apart again (bugs.md "Enabling a tool during a session
// does not enable it").
//
// Enabling is idempotent and reconciling: if the flag already says "on" but the
// registry does not hold the tool (a session started without it, a failed
// earlier build), the tool is constructed and registered now instead of
// reporting a no-op. A tool that is already registered is never replaced by a
// second instance.
func ApplyToolToggle(ctx core.Context, name string, enabled bool) (ToolToggleOutcome, error) {
	if ctx.Config == nil {
		return ToolToggleUnchanged, errToolToggleNoConfig
	}
	if getToolEnabled(ctx.Config, name) == enabled {
		if !enabled {
			return ToolToggleUnchanged, nil
		}
		// Reconcile the enable direction only: "the config says on" must imply
		// "the registry holds it", which is exactly the broken invariant.
		return enableRuntimeTool(ctx, name), nil
	}
	setToolEnabled(ctx.Config, name, enabled)
	if err := persistToolToggle(ctx, name, enabled); err != nil {
		return ToolToggleUnchanged, err
	}
	if enabled {
		return enableRuntimeTool(ctx, name), nil
	}
	disableRuntimeTool(ctx, name)
	return ToolToggleApplied, nil
}

// persistToolToggle writes the tool's enabled flag to the project config layer.
// ask_user_question is stored on the inverted clarify_disabled flag, so the
// path/value pair comes from toolSaveKeyValue.
func persistToolToggle(ctx core.Context, name string, enabled bool) error {
	if ctx.ConfigSaver == nil {
		return nil
	}
	path, value := toolSaveKeyValue(name, enabled)
	if err := ctx.ConfigSaver.SaveProjectField(path, value); err != nil {
		return &toolSaveError{err: err}
	}
	return nil
}

// enableRuntimeTool constructs the tool through ctx.ToolFactory, registers it
// and pushes the session-shaped tool set to the running agent. It reports
// ToolToggleRestartRequired when no live instance can exist (no registry, no
// factory, or a factory that cannot build the tool) so the caller can tell the
// user instead of silently reporting a success that the model cannot use.
func enableRuntimeTool(ctx core.Context, name string) ToolToggleOutcome {
	if ctx.ToolRegistry == nil {
		return ToolToggleRestartRequired
	}
	if _, ok := ctx.ToolRegistry.Get(name); ok {
		// Already live: never build a duplicate instance, just reconcile the
		// pushed set (a toggle must not re-partition the tool set).
		refreshToolRegistry(ctx)
		return ToolToggleApplied
	}
	if ctx.ToolFactory == nil {
		return ToolToggleRestartRequired
	}
	tool, ok := ctx.ToolFactory(name)
	if !ok || tool == nil {
		return ToolToggleRestartRequired
	}
	ctx.ToolRegistry.Register(tool)
	// The model is notified by AgentManager.SetTools (batched toolset-change
	// notice) — no separate injection here.
	refreshToolRegistry(ctx)
	return ToolToggleApplied
}

// disableRuntimeTool unregisters the tool, pushes the new set to the running
// agent and runs the host teardown hook (integrations bound to the tool).
func disableRuntimeTool(ctx core.Context, name string) {
	if ctx.ToolRegistry != nil {
		ctx.ToolRegistry.Unregister(name)
	}
	refreshToolRegistry(ctx)
	if ctx.ToolTeardown != nil {
		ctx.ToolTeardown(name)
	}
}

// refreshToolRegistry pushes the tool set a session would START with to the
// running agent: the host-wired mode-filtered view (core.Context.LiveTools),
// falling back to the raw registry. Pushing the mode-filtered set keeps a
// toggle from advertising tools the active mode disallows.
func refreshToolRegistry(ctx core.Context) {
	if ctx.AgentManager == nil {
		return
	}
	_ = ctx.AgentManager.SetTools(ctx.LiveToolSet())
}

func onOffLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func restartHint(enabled bool) string {
	if enabled {
		return "The tool is now available to the model."
	}
	return "The tool is no longer available to the model."
}

// parseToolOnOff maps an on/off argument to the requested state.
func parseToolOnOff(onOff string) bool {
	return strings.ToLower(onOff) == "on"
}
