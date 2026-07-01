// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/docs"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tools"
)

// DocsCommand provides access to embedded documentation.
type DocsCommand struct{}

func (c *DocsCommand) Name() string      { return "docs" }
func (c *DocsCommand) Aliases() []string { return []string{} }
func (c *DocsCommand) ShortHelp() string { return "View embedded documentation about Goa" }
func (c *DocsCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *DocsCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	var comps []core.ArgCompletion
	if ctx.DocsProvider != nil {
		list, err := ctx.DocsProvider.List()
		if err == nil {
			for _, d := range list {
				if prefix == "" || strings.HasPrefix(strings.ToLower(d.Name), strings.ToLower(prefix)) {
					comps = append(comps, core.ArgCompletion{Value: d.Name, Description: d.Description})
				}
			}
		}
	}
	return comps
}

func (c *DocsCommand) Run(ctx core.Context, args []string) error {
	return runDocsCommand(ctx, ctx.DocsProvider, args)
}

func runDocsCommand(out core.OutputWriter, dp core.DocsProvider, args []string) error {
	if dp == nil {
		if len(args) == 0 {
			return listBuiltinDocs(out)
		}
		return showBuiltinDoc(out, args[0])
	}

	if len(args) == 0 {
		return listDocs(out, dp)
	}
	return showDoc(out, dp, args[0])
}

// /tools — lists available tools and toggles optional ones
type ToolsDocCommand struct{}

func (c *ToolsDocCommand) Name() string      { return "tools" }
func (c *ToolsDocCommand) Aliases() []string { return []string{} }
func (c *ToolsDocCommand) ShortHelp() string {
	return "List, inspect, run, or toggle available tools"
}
func (c *ToolsDocCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *ToolsDocCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	if idx := strings.LastIndex(prefix, ":"); idx >= 0 {
		return completeToolToggleSuffix(prefix[:idx], prefix[idx+1:])
	}
	return completeToolNames(ctx, prefix)
}

func completeToolToggleSuffix(name, suffix string) []core.ArgCompletion {
	if !isConfigurableTool(name) {
		return nil
	}
	var comps []core.ArgCompletion
	for _, v := range []string{"on", "off"} {
		if strings.HasPrefix(v, suffix) {
			comps = append(comps, core.ArgCompletion{Value: name + ":" + v, Description: "toggle " + name})
		}
	}
	return comps
}

func completeToolNames(ctx core.Context, prefix string) []core.ArgCompletion {
	var comps []core.ArgCompletion
	for _, name := range tools.ConfigurableToolNames() {
		if prefix == "" || strings.HasPrefix(name, prefix) {
			comps = append(comps, core.ArgCompletion{Value: name, Description: toolStatusLabel(ctx, name)})
		}
	}
	if ctx.ToolRegistry != nil {
		for _, t := range ctx.ToolRegistry.All() {
			schema := t.Schema()
			if prefix == "" || strings.HasPrefix(schema.Name, prefix) {
				comps = append(comps, core.ArgCompletion{Value: schema.Name, Description: schema.Description})
			}
		}
	}
	return comps
}

func (c *ToolsDocCommand) Run(ctx core.Context, args []string) error {
	if len(args) == 0 {
		return listTools(ctx, ctx.ToolRegistry)
	}

	// Parse "name:on" / "name:off" forms.
	name, onOff, ok := parseToolToggleArgs(args)
	if ok {
		return toggleTool(ctx, name, onOff)
	}

	// Single arg: show tool detail or help
	if len(args) == 1 && !strings.Contains(args[0], "=") {
		return showToolDetail(ctx, ctx.ToolRegistry, args[0])
	}

	// Tool execution: /tools:<name>:<key>=<value>,<key>=<value>,...
	// The router splits on colon, so args = [name, "key=val,key=val,..."]
	return runToolDirect(ctx, args)
}

// parseValue auto-detects booleans and numbers from a string value.
func parseValue(val string) any {
	if b, err := strconv.ParseBool(val); err == nil {
		return b
	}
	if n, err := strconv.Atoi(val); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(val, 64); err == nil {
		return f
	}
	return val
}

// handleBareValue stores a key=val pair without '=' into the params map.
// First bare value becomes "pattern"; subsequent ones become boolean flags.
func handleBareValue(params map[string]any, pair string) {
	if _, exists := params["pattern"]; !exists {
		params["pattern"] = pair
	} else {
		params[pair] = true
	}
}

// parseKeyValuePairs converts "key=val,key2=val2" into a map, handling
// bare values (no '=') as the "pattern" key and auto-parsing numbers/bools.
func parseKeyValuePairs(raw string) map[string]any {
	params := make(map[string]any)
	if raw == "" {
		return params
	}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		eqIdx := strings.IndexByte(pair, '=')
		if eqIdx < 0 {
			handleBareValue(params, pair)
			continue
		}
		key := strings.TrimSpace(pair[:eqIdx])
		val := strings.TrimSpace(pair[eqIdx+1:])
		if key == "" {
			continue
		}
		params[key] = parseValue(val)
	}
	return params
}

// runToolDirect executes a tool directly with parameters from the command line.
func runToolDirect(ctx core.Context, args []string) error {
	if len(args) < 2 {
		writeStr(ctx, "Usage: /tools:<tool-name>:<param1>=<value1>,<param2>=<value2>,...\n")
		return nil
	}

	toolName := args[0]

	if ctx.ToolRegistry == nil {
		writeFmt(ctx, "Tool registry not available.\n")
		return nil
	}
	tool, ok := ctx.ToolRegistry.Get(toolName)
	if !ok {
		writeFmt(ctx, "Tool %q not found.\n", toolName)
		return nil
	}

	schema := tool.Schema()
	params := parseKeyValuePairs(args[1])

	// Validate required parameters from schema
	missing := missingRequiredParams(schema, params)
	if len(missing) > 0 {
		writeFmt(ctx, "Missing required parameter(s): %s\n", strings.Join(missing, ", "))
		writeFmt(ctx, "Usage: /tools:%s:<key>=<value>,...\n", toolName)
		printParamHelp(ctx, schema, missing)
		return nil
	}

	inputBytes, err := json.Marshal(params)
	if err != nil {
		writeFmt(ctx, "Failed to build tool input: %v\n", err)
		return nil
	}

	input := string(inputBytes)
	writeFmt(ctx, "⚡ Running %s...\n", toolName)
	result, err := tool.Execute(input)
	if err != nil {
		writeFmt(ctx, "%s error: %v\n", toolName, err)
		return nil
	}

	writeStr(ctx, result)
	if !strings.HasSuffix(result, "\n") {
		writeStr(ctx, "\n")
	}
	return nil
}

// missingRequiredParams returns param names declared required in schema that
// are absent from params.
func missingRequiredParams(schema agentic.ToolSchema, params map[string]any) []string {
	if schema.Schema == nil {
		return nil
	}
	reqList, ok := schema.Schema["required"].([]any)
	if !ok {
		return nil
	}
	var missing []string
	for _, r := range reqList {
		name, ok := r.(string)
		if !ok {
			continue
		}
		if _, exists := params[name]; !exists {
			missing = append(missing, name)
		}
	}
	return missing
}

// printParamHelp shows expected parameter details for missing params.
func printParamHelp(ctx core.Context, schema agentic.ToolSchema, missing []string) {
	props, ok := schema.Schema["properties"].(map[string]any)
	if !ok {
		return
	}
	for _, name := range missing {
		p, ok := props[name].(map[string]any)
		if !ok {
			continue
		}
		typ, _ := p["type"].(string)
		desc, _ := p["description"].(string)
		writeFmt(ctx, "  --%-20s %s  %s\n", name, typ, desc)
	}
}

// parseToolToggleArgs extracts a tool name and on/off state from arguments.
func parseToolToggleArgs(args []string) (name, onOff string, ok bool) {
	if len(args) == 1 {
		if idx := strings.LastIndex(args[0], ":"); idx >= 0 {
			name = args[0][:idx]
			onOff = args[0][idx+1:]
			if isConfigurableTool(name) && isToolToggleOnOff(onOff) {
				return name, onOff, true
			}
			return "", "", false
		}
	}
	if len(args) == 2 {
		name = args[0]
		onOff = args[1]
		if isConfigurableTool(name) && isToolToggleOnOff(onOff) {
			return name, onOff, true
		}
		return "", "", false
	}
	return "", "", false
}

func isToolToggleOnOff(s string) bool {
	switch strings.ToLower(s) {
	case "on", "off":
		return true
	}
	return false
}

// isConfigurableTool reports whether name is a runtime-toggleable tool.
func isConfigurableTool(name string) bool {
	for _, n := range tools.ConfigurableToolNames() {
		if n == name {
			return true
		}
	}
	return false
}

// toolStatusLabel returns a short description of the configured state.
func toolStatusLabel(ctx core.Context, name string) string {
	if ctx.Config == nil {
		return "unknown"
	}
	enabled := getToolEnabled(ctx.Config, name)
	if enabled {
		return "enabled"
	}
	return "disabled"
}

// getToolEnabled reads the enabled flag for a configurable tool.
func getToolEnabled(cfg *config.Config, name string) bool {
	switch name {
	case "bg_exec":
		return cfg.Tools.Enabled.BGExec
	case "delegate_to":
		return cfg.Tools.Enabled.DelegateTo
	case "memento":
		return cfg.Tools.Enabled.Memento
	case "pty_exec":
		return cfg.Tools.Enabled.PTYExec
	case "request_review":
		return cfg.Tools.Enabled.RequestReview
	case "ssh_bash":
		return cfg.Tools.Enabled.SSHBash
	}
	return false
}

// setToolEnabled updates the enabled flag for a configurable tool.
func setToolEnabled(cfg *config.Config, name string, enabled bool) {
	cfg.Tools.Enabled.SetEnabled(name, enabled)
}

// toggleTool enables or disables a configurable tool, persists the change,
// and updates the active agent's tool list if possible.
func toggleTool(ctx core.Context, name, onOff string) error {
	if ctx.Config == nil {
		return fmt.Errorf("configuration not available")
	}

	enabled := strings.ToLower(onOff) == "on"
	wasEnabled := getToolEnabled(ctx.Config, name)

	if wasEnabled == enabled {
		ctx.Writef("Tool %s is already %s.\n", name, onOffLabel(enabled))
		return nil
	}

	setToolEnabled(ctx.Config, name, enabled)

	if ctx.ConfigSaver != nil {
		if err := ctx.ConfigSaver.SaveHomeField([]string{"tools", "enabled", name}, enabled); err != nil {
			ctx.Writef("Failed to save config: %v\n", err)
			return nil
		}
	}

	if enabled {
		// Instantiate and register the tool at runtime so the model can use
		// it on the next turn without restarting the session.
		if ctx.ToolFactory != nil {
			tool, ok := ctx.ToolFactory(name)
			if !ok {
				ctx.Writef("Tool %s could not be instantiated at runtime. Restart Goa to apply the change.\n", name)
				return nil
			}
			ctx.ToolRegistry.Register(tool)
		}
		if ctx.AgentManager != nil {
			_ = ctx.AgentManager.SetTools(ctx.ToolRegistry.All())
			_ = ctx.AgentManager.InjectSystemMessage(fmt.Sprintf("A new tool is now available to you: %s. You may use it on subsequent turns.", name))
		}
	} else {
		// Remove the tool from the runtime registry immediately so the model
		// cannot call it on the next turn.
		ctx.ToolRegistry.Unregister(name)
		if ctx.AgentManager != nil {
			_ = ctx.AgentManager.SetTools(ctx.ToolRegistry.All())
		}
	}

	ctx.Writef("Tool %s %s. %s\n", name, onOffLabel(enabled), restartHint(enabled))

	// Notify the user (and indirectly the model on the next turn) that the
	// available tool set has changed.
	ctx.Flash(fmt.Sprintf("Tool %s %s", name, onOffLabel(enabled)))
	return nil
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

func showToolDetail(out core.OutputWriter, reg core.ToolRegistry, name string) error {
	if reg == nil {
		writeStr(out, "Tool registry not available.\n")
		return nil
	}
	tool, found := reg.Get(name)
	if !found {
		writeFmt(out, "Tool not found: %s\n", name)
		writeStr(out, "Use /tools:to list available tools.\n")
		return nil
	}
	schema := tool.Schema()
	writeFmt(out, "🔧 Tool: %s\n", schema.Name)
	writeStr(out, "────────────────────────────────────────\n")
	writeFmt(out, "  %s\n\n", schema.Description)

	printToolParams(out, schema)
	printToolDocs(out, tool)

	// Show execution example
	props, _ := schema.Schema["properties"].(map[string]any)
	if props != nil {
		writeStr(out, "Execute:\n")
		writeFmt(out, "  /tools:%s:<key>=<value>,...\n\n", schema.Name)
	}
	return nil
}

func printToolParams(out core.OutputWriter, schema agentic.ToolSchema) {
	if schema.Schema == nil {
		return
	}
	props, _ := schema.Schema["properties"].(map[string]any)
	if props == nil {
		return
	}
	required := extractRequired(schema.Schema)
	writeStr(out, "Parameters:\n")
	for paramName, paramVal := range props {
		p, _ := paramVal.(map[string]any)
		desc, _ := p["description"].(string)
		typ, _ := p["type"].(string)
		reqStr := ""
		if _, isReq := required[paramName]; isReq {
			reqStr = " (required)"
		}
		writeFmt(out, "  %-15s %s%s\n", paramName, typ, reqStr)
		if desc != "" {
			writeFmt(out, "     %s\n", desc)
		}
	}
	writeStr(out, "\n")
}

func extractRequired(schema map[string]any) map[string]bool {
	result := make(map[string]bool)
	reqList, ok := schema["required"].([]any)
	if !ok {
		return result
	}
	for _, r := range reqList {
		if name, ok := r.(string); ok {
			result[name] = true
		}
	}
	return result
}

func printToolDocs(out core.OutputWriter, tool agentic.Tool) {
	doc, ok := tool.(tools.Documentable)
	if !ok {
		return
	}
	if ld := doc.LongDoc(); ld != "" {
		writeFmt(out, "Description:\n  %s\n\n", strings.ReplaceAll(ld, "\n", "\n  "))
	}
	if ex := doc.Examples(); len(ex) > 0 {
		writeStr(out, "Examples:\n")
		for _, e := range ex {
			writeFmt(out, "  %s\n", e)
		}
	}
}

func listTools(out core.OutputWriter, reg core.ToolRegistry) error {
	writeStr(out, "🔧 Goa Tools\n")
	writeStr(out, "────────────────────────────────────────\n\n")

	if reg == nil {
		knownTools := []string{"read", "write", "edit", "search", "bash", "ssh_bash", "bg_exec", "memento", "goa_command", "run_skill"}
		for _, t := range knownTools {
			writeFmt(out, "  %-20s (use /tools %s for details)\n", t, t)
		}
		writeStr(out, "\n10 tool(s). Tool registry not wired. Use /docs:TOOLS for full reference.\n")
		return nil
	}

	tools := reg.All()
	for _, t := range tools {
		schema := t.Schema()
		desc := schema.Description
		if len(desc) > 60 {
			desc = desc[:57] + "..."
		}
		writeFmt(out, "  %-20s %s\n", schema.Name, desc)
	}
	writeFmt(out, "\n%d tool(s) available. Use /tools:name for details, or /docs:TOOLS for the full reference.\n", len(tools))
	return nil
}

func listDocs(out core.OutputWriter, dp core.DocsProvider) error {
	docList, err := dp.List()
	if err != nil {
		writeErr(out, "Error listing docs: %v", err)
		return nil
	}

	writeStr(out, "📚 Goa Documentation\n")
	writeStr(out, "────────────────────────────────────────\n\n")
	for _, d := range docList {
		writeFmt(out, "  %-25s %s\n", d.Name, d.Description)
	}
	writeStr(out, "\nUse /docs:name to view a document.")
	return nil
}

func showDoc(out core.OutputWriter, dp core.DocsProvider, query string) error {
	info, err := dp.FindDocFile(query)
	if err != nil {
		writeFmt(out, "Documentation not found: %s\n", query)
		writeStr(out, "Use /docs:to list available documents.\n")
		return nil
	}

	content, err := dp.Get(info.Name)
	if err != nil {
		writeFmt(out, "Error reading %s: %v\n", info.Name, err)
		return nil
	}

	writeFmt(out, "📖 %s\n", info.Path)
	writeStr(out, "────────────────────────────────────────\n\n")
	writeStr(out, content+"\n")
	return nil
}

// listBuiltinDocs is a fallback when DocsProvider is not wired.
func listBuiltinDocs(out core.OutputWriter) error {
	docList, err := docs.List()
	if err != nil {
		writeErr(out, "Error listing docs: %v", err)
		return nil
	}

	writeStr(out, "📚 Goa Documentation\n")
	writeStr(out, "────────────────────────────────────────\n\n")
	for _, d := range docList {
		writeFmt(out, "  %-25s %s\n", d.Name, d.Description)
	}
	writeStr(out, "\nUse /docs:name to view a document.")
	return nil
}

// showBuiltinDoc is a fallback when DocsProvider is not wired.
func showBuiltinDoc(out core.OutputWriter, name string) error {
	content, err := docs.Get(name)
	if err != nil {
		writeFmt(out, "Documentation not found: %s\n", name)
		writeStr(out, "Use /docs:to list available documents.\n")
		return nil
	}

	info, _ := docs.FindDocFile(name)
	path := info.Path
	if path == "" {
		path = "docs/" + strings.ToUpper(name) + ".md"
	}
	writeFmt(out, "📖 %s\n", path)
	writeStr(out, "────────────────────────────────────────\n\n")
	writeStr(out, content+"\n")
	return nil
}
