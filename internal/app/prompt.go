// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/docs"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/memory"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tools"
	"github.com/pijalu/goa/tui"
)

// buildSystemPrompt assembles the complete system prompt from all sources:
//  1. Mode system prompt (coder/planner/reviewer)
//  2. AGENTS.md context files (wrapped in <project_context>)
//  3. Long-term memory summaries (wrapped in <memory>)
//  4. Active skill listings from the current mode's skill stack (name,
//     description, location — not the full body)
//  5. Available skills listing (wrapped in <available_skills>)
//  6. Self-documentation links (goa:// references for the LLM)
//  7. Agent-driven prompts (if enabled, added by agentmanager)
//
// The assembled prompt is bounded by a context-window-aware budget. Low-priority
// sections are dropped or truncated first so the prompt stays within a safe
// fraction of the model's context window, leaving room for tools and the
// conversation. Context is expensive for every model, so the budget is kept as
// small as possible regardless of how large the context window is.
func buildSystemPrompt(subs *subsystems) string {
	var parts []string

	ctxWindow := modelContextWindow(subs)
	budget := systemPromptBudget(ctxWindow)

	// 1. Mode system prompt (base) — read from agent runtime state, not config
	if body := modeSystemPrompt(subs); body != "" {
		parts = append(parts, body)
	}

	// 1a. Tool usage principles — global guidance shared by all modes.
	if toolUsage := buildToolUsageSection(); toolUsage != "" {
		parts = append(parts, toolUsage)
	}

	// 2. AGENTS.md context files
	if len(subs.contextFiles) > 0 {
		parts = append(parts, renderContextFiles(subs.contextFiles))
	}

	// 3. Active skill listings from the current mode's skill stack.
	// The full skill body is not inlined; the agent loads a skill via the read
	// tool when the task matches it. This follows the pi approach and keeps the
	// initial prompt small.
	if activeSkills := buildActiveSkillsSection(subs, budget); activeSkills != "" {
		parts = append(parts, activeSkills)
	}

	// 4. Long-term memory summaries — lower priority than active skills
	// because memories can be read on demand.
	if memSection := buildMemorySection(subs); memSection != "" {
		parts = append(parts, memSection)
	}

	// 5. Available skills (all skills listed regardless of inline vs sub-agent)
	if rendered := availableSkillsSection(subs); rendered != "" {
		parts = append(parts, rendered)
	}

	// 6. Self-documentation links (single source of truth — generated from embedded docs)
	if selfDoc := buildSelfDocSection(); selfDoc != "" {
		parts = append(parts, selfDoc)
	}

	prompt := strings.Join(parts, "\n\n")
	if budget > 0 && len(prompt) > budget {
		prompt = applySystemPromptBudget(parts, budget)
	}
	return prompt
}

// modelContextWindow returns the effective context window for the active model.
// It prefers any runtime override, then the provider's resolved model. For
// local providers whose loaded length is not yet known, it falls back to a
// conservative default (8k) so the system prompt does not exceed a realistic
// local-server context. A zero window disables the budget only for providers
// whose context window cannot be inferred.
func modelContextWindow(subs *subsystems) int {
	if subs.ContextWindow > 0 {
		return subs.ContextWindow
	}
	if subs.providerMgr == nil {
		return 0
	}
	mdl, err := subs.providerMgr.ResolveActiveModel()
	if err != nil {
		return 0
	}
	if mdl.ContextWindow > 0 {
		return mdl.ContextWindow
	}
	// Local servers often advertise the model max before the model is loaded;
	// use a safe fallback so the prompt is budgeted for a typical loaded context.
	if subs.providerMgr.IsLocalProvider() {
		return 8192
	}
	return 0
}

// systemPromptBudget returns a character budget for the system prompt based on
// the model's context window. A zero or negative window disables the budget.
// The budget is intentionally conservative and capped: context is expensive for
// every model, so the system prompt is kept as short as possible regardless of
// how large the context window is.
func systemPromptBudget(ctxWindow int) int {
	if ctxWindow <= 0 {
		return 0 // unlimited
	}
	const charsPerToken = 4
	// Keep the system prompt small even on large models. Tools, the user
	// message, and the assistant response consume the rest of the context.
	switch {
	case ctxWindow <= 8192:
		// 8k context: keep the mode prompt, project context, and a compact
		// active-skills list; drop available skills, self-doc, and excess memory.
		return 6000
	case ctxWindow <= 16384:
		return 9000
	case ctxWindow <= 32768:
		return 14000
	case ctxWindow <= 65536:
		return 20000
	default:
		return min(ctxWindow*20/100*charsPerToken, 30000)
	}
}

// applySystemPromptBudget trims the system prompt until it fits the budget.
// Sections are dropped in order of lowest priority first (the last section
// is dropped first). If even the highest-priority section exceeds the budget,
// it is truncated with an ellipsis.
func applySystemPromptBudget(parts []string, budget int) string {
	for len(parts) > 1 && len(strings.Join(parts, "\n\n")) > budget {
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 1 && len(parts[0]) > budget {
		parts[0] = truncateToBudget(parts[0], budget)
	}
	return strings.Join(parts, "\n\n")
}

// truncateToBudget truncates text to a target length, preferring to break at a
// newline and appending a truncation marker.
func truncateToBudget(text string, budget int) string {
	if len(text) <= budget {
		return text
	}
	if budget <= 50 {
		return text[:max(0, budget)] + "…"
	}
	cutoff := budget - 50
	if idx := strings.LastIndex(text[:cutoff], "\n"); idx > cutoff/2 {
		cutoff = idx
	}
	return strings.TrimSpace(text[:cutoff]) + "\n\n[…additional instructions truncated]"
}

// modeSystemPrompt returns the system prompt for the active major mode.
func modeSystemPrompt(subs *subsystems) string {
	if subs.modeRegistry == nil {
		return ""
	}
	mode := currentMajorMode(subs)
	return subs.modeRegistry.SystemPrompt(mode)
}

// currentMajorMode returns the effective major mode, preferring the agent
// manager's runtime state and falling back to configuration.
func currentMajorMode(subs *subsystems) internal.MajorMode {
	if subs.agentMgr != nil {
		if mode := subs.agentMgr.CurrentMode().Major; mode != "" {
			return mode
		}
	}
	return subs.cfg.DefaultModeState().Major
}

// renderContextFiles wraps AGENTS.md context files in a <project_context> block.
func renderContextFiles(files []internal.ContextFile) string {
	var ctxParts []string
	ctxParts = append(ctxParts, "<project_context>")
	for _, cf := range files {
		ctxParts = append(ctxParts, cf.Content)
	}
	ctxParts = append(ctxParts, "</project_context>")
	return strings.Join(ctxParts, "\n\n")
}

// availableSkillsSection renders the <available_skills> listing when skills exist.
func availableSkillsSection(subs *subsystems) string {
	skillSummaries := subs.skillRegistry.List()
	if len(skillSummaries) == 0 {
		return ""
	}
	globalModeInline := subs.cfg != nil && subs.cfg.Skills.ExecutionMode == config.AgenticSkillModeInline
	skillSummaries = filterSkillsForMode(skillSummaries, globalModeInline)
	if len(skillSummaries) == 0 {
		return ""
	}
	return skills.RenderAvailableSkills(subs.promptReg, skillSummaries, runSkillToolAvailable(subs))
}

// runSkillToolAvailable reports whether the run_skill tool is registered,
// i.e. whether action skills can be invoked via run_skill in this session.
func runSkillToolAvailable(subs *subsystems) bool {
	if subs.toolRegistry == nil {
		return false
	}
	_, ok := subs.toolRegistry.Get("run_skill")
	return ok
}

// buildSelfDocSection returns a minimal <goa_documentation> section that lists
// embedded documents and how to read them via the goa:// scheme. It is
// intentionally short because context is expensive for every model.
func buildSelfDocSection() string {
	docList, err := docs.List()
	if err != nil || len(docList) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<goa_documentation>\n")
	b.WriteString("Read embedded docs via goa:// URLs.")
	for _, d := range docList {
		b.WriteByte(' ')
		b.WriteString(d.Name)
	}
	b.WriteString("\n</goa_documentation>")
	return b.String()
}

// buildToolUsageSection returns a <tool_usage> paragraph with general
// guidance about how to use the available tools. This section is shared
// by all modes and injected early in the system prompt.
func buildToolUsageSection() string {
	return `<tool_usage>
Prefer dedicated tools over bash.
</tool_usage>`
}

// showStartupBanner displays startup information in the chat viewport:
// what context file was loaded (if any) and how many skills are available.
func showStartupBanner(subs *subsystems, chat *tui.ChatViewport) {
	// Context file info
	if len(subs.contextFiles) > 0 {
		lastFile := subs.contextFiles[len(subs.contextFiles)-1]
		chat.AddSystemMessage(fmt.Sprintf("⟡ Context loaded: %s", lastFile.Path))
	} else {
		chat.AddSystemMessage("⟡ No AGENTS.md context file found")
	}

	// Skills summary
	skillList := subs.skillRegistry.List()
	if len(skillList) > 0 {
		showSkillBanner(subs, chat, skillList)
	} else {
		chat.AddSystemMessage("⟡ No skills loaded")
	}

	// LSP (gopls) availability — surface start failures instead of staying
	// silent so the user knows Go diagnostics are unavailable (bugs.md L1).
	showLSPBanner(subs, chat)
}

// showLSPBanner reports gopls availability. It only speaks when LSP was
// expected (a Go project) but failed to start; a running LSP is the quiet
// default and needs no banner.
func showLSPBanner(subs *subsystems, chat *tui.ChatViewport) {
	if subs.lspMgr == nil {
		return
	}
	if err := subs.lspMgr.StartError(); err != nil {
		chat.AddSystemMessage(fmt.Sprintf("⟡ LSP (gopls) unavailable: %v — Go diagnostics on write/edit are disabled", err))
	}
}

func showSkillBanner(subs *subsystems, chat *tui.ChatViewport, skillList []skills.SkillSummary) {
	globalModeInline := subs.cfg != nil && subs.cfg.Skills.ExecutionMode == config.AgenticSkillModeInline

	skillList = filterSkillsForMode(skillList, globalModeInline)
	inlineCount, forcedInlineCount, subCount := countSkillModes(skillList, globalModeInline)

	globalMode := "sub-agent"
	if globalModeInline {
		globalMode = "inline"
	}

	if forcedInlineCount > 0 {
		chat.AddSystemMessage(fmt.Sprintf("⟡ %d skills (%d inline, %d forced inline · mode: %s)",
			len(skillList), inlineCount, forcedInlineCount, globalMode))
	} else {
		chat.AddSystemMessage(fmt.Sprintf("⟡ %d skills (%d inline, %d sub-agent · mode: %s)",
			len(skillList), inlineCount, subCount, globalMode))
	}
}

// filterSkillsForMode removes skills that require a sub-agent when the global
// execution mode is inline. Skills with sub-skills are only available in
// sub-agent mode.
func filterSkillsForMode(skillList []skills.SkillSummary, globalModeInline bool) []skills.SkillSummary {
	if !globalModeInline {
		return skillList
	}
	filtered := make([]skills.SkillSummary, 0, len(skillList))
	for _, s := range skillList {
		if !s.RequiresSubAgent {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func countSkillModes(skillList []skills.SkillSummary, globalModeInline bool) (inline, forcedInline, sub int) {
	for _, s := range skillList {
		if s.Inline {
			inline++
		} else if globalModeInline {
			forcedInline++
		} else {
			sub++
		}
	}
	return
}

func startAgentSession(subs *subsystems, chat *tui.ChatViewport) {
	providerCfg, model := subs.providerMgr.Active()
	if providerCfg == nil {
		chat.AddSystemMessage("No provider configured. Type /setup to configure.")
		subs.tuiEngine.RequestRender()
		return
	}

	subs.footer.SetData(tui.FooterData{
		Workdir:                subs.projectDir,
		Model:                  activeModelDisplay(subs),
		Provider:               providerCfg.ID,
		Profile:                string(subs.effectiveModeState().Major),
		Mode:                   string(subs.effectiveModeState().Autonomy),
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	})
	subs.tuiEngine.RequestRender()

	mdl, err := subs.providerMgr.ResolveActiveModel()
	if err != nil {
		chat.AddSystemMessage(fmt.Sprintf("Failed to resolve model: %v", err))
		subs.tuiEngine.RequestRender()
		return
	}
	// NOTE: no synchronous RefreshLocalContextWindow here — that performs up
	// to 3 HTTP probes (5s timeout each) against the local server and would
	// block startup before the first frame (bugs.md Start-up: no blocking
	// HTTP/API on the startup path). The real loaded context length is
	// re-detected asynchronously after the first response delta via
	// AgentManager.maybeRefreshContextWindow, which updates the agent and
	// emits EventContextStats for the footer. Until then the registry/config
	// context window is the documented fallback.
	streamOpts := subs.providerMgr.BuildStreamOptions()

	systemPrompt := buildSystemPrompt(subs)
	agenticTools := filterToolsForCurrentMode(subs, subs.toolRegistry.All())
	_, err = subs.agentMgr.StartSession(mdl, streamOpts, systemPrompt, agenticTools, subs.cfg)
	if err != nil {
		chat.AddSystemMessage(fmt.Sprintf("Failed to start session: %v", err))
		subs.tuiEngine.RequestRender()
		return
	}

	// Wire main agent into the foreground orchestrator
	if subs.foregroundOrch != nil {
		mainAgent := subs.agentMgr.CurrentAgent()
		if mainAgent != nil {
			subs.foregroundOrch.SetMainAgent(mainAgent)
		}
	}

	maybeInjectBashComplexityNotice(subs, chat)

	providerName := providerCfg.Name
	if providerName == "" {
		providerName = providerCfg.ID
	}
	msg := fmt.Sprintf("⟡ Connected to %s (%s).", providerName, model)
	chat.AddSystemMessage(msg)
	subs.tuiEngine.RequestRender()
}

// maybeInjectBashComplexityNotice sends a durable system message to the LLM
// when bash complexity analysis is enabled and the conversation already has
// user/assistant messages (i.e. the setting is being applied to an ongoing
// conversation). When the conversation is fresh, the tool description
// already informs the agent of the restriction.
func maybeInjectBashComplexityNotice(subs *subsystems, chat *tui.ChatViewport) {
	if subs.toolRegistry == nil || subs.agentMgr == nil {
		return
	}
	bashToolIface, ok := subs.toolRegistry.Get("bash")
	if !ok {
		return
	}
	bashTool, ok := bashToolIface.(*tools.BashTool)
	if !ok || !bashTool.EnableComplexity {
		return
	}
	if !conversationHasUserOrAssistant(chat) {
		return
	}
	if err := subs.agentMgr.InjectSystemMessage(bashTool.ComplexityNotice()); err != nil {
		chat.AddSystemMessage(fmt.Sprintf("[goa-system] Failed to notify agent of bash complexity analysis: %v", err))
	}
}

// conversationHasUserOrAssistant returns true when the chat already contains
// at least one user or assistant message, indicating an ongoing conversation.
func conversationHasUserOrAssistant(chat *tui.ChatViewport) bool {
	for _, m := range chat.Snapshot() {
		if m.Type == tui.ConsoleUserMessage || m.Type == tui.ConsoleAssistantMessage {
			return true
		}
	}
	return false
}

// filterToolsForCurrentMode returns the subset of tools allowed by the
// active mode's AllowedTools list. When the mode has no AllowedTools
// restriction (empty list), all tools are returned unfiltered.
func filterToolsForCurrentMode(subs *subsystems, allTools []agentic.Tool) []agentic.Tool {
	if subs.modeRegistry == nil || subs.agentMgr == nil {
		return allTools
	}
	mode := subs.agentMgr.CurrentMode().Major
	if mode == "" {
		return allTools
	}
	spec, err := subs.modeRegistry.Resolve(mode)
	if err != nil || len(spec.AllowedTools) == 0 {
		return allTools
	}
	allowed := make(map[string]bool, len(spec.AllowedTools))
	for _, name := range spec.AllowedTools {
		allowed[name] = true
	}
	filtered := make([]agentic.Tool, 0, len(allTools))
	for _, t := range allTools {
		if allowed[t.Schema().Name] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// buildActiveSkillsSection lists the skills loaded into the current mode's
// skill stack in compact form: name and file location.
// The full skill body is not inlined in the system prompt; the agent loads a
// skill with the read tool when the task matches it. This follows the pi
// approach and keeps the initial prompt as short as possible.
func buildActiveSkillsSection(subs *subsystems, budget int) string {
	if subs.agentMgr == nil || subs.skillRegistry == nil {
		return ""
	}
	mode := subs.agentMgr.CurrentMode()
	if len(mode.Skills) == 0 {
		return ""
	}

	type item struct {
		name     string
		location string
	}
	var items []item
	for _, name := range mode.Skills {
		skill, ok := subs.skillRegistry.Get(name)
		if !ok {
			continue
		}
		items = append(items, item{
			name:     escapeXML(skill.Meta.Name),
			location: escapeXML(skill.FilePath),
		})
	}
	if len(items) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<active_skills>\n")
	b.WriteString("Active skills: ")
	for i, it := range items {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s (%s)", it.name, it.location)
	}
	b.WriteString("\n</active_skills>")
	return b.String()
}

// escapeXML escapes special XML characters so rendered skill metadata is safe
// for inclusion in the system prompt.
func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

// buildMemorySection constructs a <memory> block from memory file summaries.
// It only includes summaries (not full content) to avoid context overload.
// Memories are sorted by recency and included until the token budget is
// exhausted. The agent can read the full memory file via read_file if a
// summary is relevant.
//
// If a consolidated dream memory exists, it is used as the only memory entry.
func buildMemorySection(subs *subsystems) string {
	if !subs.MemoryEnabled || subs.memStore == nil {
		return ""
	}

	files, err := subs.memStore.List()
	if err != nil || len(files) == 0 {
		return ""
	}

	budget := subs.MemoryBudget
	if budget <= 0 {
		budget = defaultMemoryBudget(subs)
	}

	var entries []string
	var used int

	if subs.memStore.HasConsolidated() {
		entries, used = collectConsolidatedAndRecent(subs.memStore, files, budget)
	} else {
		entries, used = collectMemoryEntries(subs.memStore, files, budget)
	}
	if len(entries) == 0 {
		return ""
	}
	_ = used

	return renderMemorySection(entries)
}

func collectConsolidatedAndRecent(memStore *memory.MemoryStore, files []memory.MemoryFileInfo, budget int) ([]string, int) {
	content, err := memStore.ReadConsolidated()
	if err != nil {
		log.Printf("Warning: cannot read consolidated memory: %v", err)
		return collectMemoryEntries(memStore, files, budget)
	}

	summary := extractMemorySummary(content)
	if summary == "" {
		log.Println("Warning: consolidated memory has no summary; using full content.")
		summary = strings.TrimSpace(content)
	}
	entry := fmt.Sprintf("Consolidated (read full file for details):\n%s", summary)
	tokens := estimateTokens(entry)
	if tokens >= budget {
		return []string{entry}, tokens
	}

	entries := []string{entry}
	used := tokens

	consolidatedPath := memStore.ConsolidatedPath()
	consolidatedMtime := consolidatedModTime(consolidatedPath)

	for _, f := range files {
		if consolidatedMtime.IsZero() || f.Mtime.Before(consolidatedMtime) {
			continue
		}
		mc, err := memStore.Read(stripGlobalSuffix(f.Name))
		if err != nil {
			log.Printf("Warning: cannot read memory %q: %v", f.Name, err)
			continue
		}
		ms := extractMemorySummary(mc)
		if ms == "" {
			continue
		}
		t := estimateTokens(ms)
		if used+t > budget {
			break
		}
		used += t
		entries = append(entries, fmt.Sprintf("%s: %s", f.Name, ms))
	}
	return entries, used
}

func consolidatedModTime(path string) time.Time {
	if path == "" {
		return time.Time{}
	}
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime()
	}
	return time.Time{}
}

func collectMemoryEntries(memStore *memory.MemoryStore, files []memory.MemoryFileInfo, budget int) ([]string, int) {
	var entries []string
	used := 0
	for _, f := range files {
		content, err := memStore.Read(stripGlobalSuffix(f.Name))
		if err != nil {
			log.Printf("Warning: cannot read memory %q: %v", f.Name, err)
			continue
		}
		summary := extractMemorySummary(content)
		if summary == "" {
			log.Printf("Warning: memory %q has no summary; skipping. Use read_file to access full content.", f.Name)
			continue
		}
		tokens := estimateTokens(summary)
		if used+tokens > budget {
			log.Printf("Warning: memory budget exhausted after %d entries; remaining memories skipped.", len(entries))
			break
		}
		used += tokens
		entries = append(entries, fmt.Sprintf("%s: %s", f.Name, summary))
	}
	return entries, used
}

func renderMemorySection(entries []string) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<memory>\n")
	b.WriteString("Summaries of long-term memories. Read full files with read_file if relevant.\n\n")
	b.WriteString(strings.Join(entries, "\n\n"))
	b.WriteString("\n</memory>")
	return b.String()
}

// stripGlobalSuffix removes the " (global)" suffix from memory store display
// names so Read can locate the file.
func stripGlobalSuffix(name string) string {
	return strings.TrimSuffix(name, " (global)")
}

// extractMemorySummary pulls the summary from a memory markdown file.
// Supported formats:
//   - YAML frontmatter with a "summary:" key.
//   - A "## Summary" section followed by content.
//
// If no summary is found, an empty string is returned and the memory is not
// injected.
func extractMemorySummary(content string) string {
	// Try YAML frontmatter first.
	if summary := extractFrontmatterSummary(content); summary != "" {
		return summary
	}
	// Fall back to a "## Summary" section.
	return extractSectionSummary(content)
}

func extractFrontmatterSummary(content string) string {
	lines := strings.Split(content, "\n")
	inFrontmatter := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if inFrontmatter {
				break
			}
			inFrontmatter = true
			continue
		}
		if !inFrontmatter {
			return ""
		}
		if strings.HasPrefix(trimmed, "summary:") {
			summary := strings.TrimSpace(strings.TrimPrefix(trimmed, "summary:"))
			// Handle multi-line folded summaries by reading until next key or end.
			return summary
		}
	}
	return ""
}

func extractSectionSummary(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "## Summary" {
			var parts []string
			for j := i + 1; j < len(lines); j++ {
				l := lines[j]
				if strings.HasPrefix(strings.TrimSpace(l), "##") {
					break
				}
				parts = append(parts, l)
			}
			return strings.TrimSpace(strings.Join(parts, "\n"))
		}
	}
	return ""
}

// estimateTokens returns a rough token count for budget enforcement.
// It uses chars/4 for ASCII text, which is conservative enough for summaries.
func estimateTokens(text string) int {
	n := 0
	for _, r := range text {
		if r > 127 {
			n += 2
		} else {
			n++
		}
	}
	return n / 4
}

// defaultMemoryBudget returns a sensible memory token budget when none is set.
// It defaults to 1024 tokens but never exceeds 10% of the active model's
// context window to avoid crowding the conversation.
func defaultMemoryBudget(subs *subsystems) int {
	const defaultBudget = 1024
	const maxRatio = 0.1

	if subs.providerMgr == nil {
		return defaultBudget
	}
	if mdl, err := subs.providerMgr.ResolveActiveModel(); err == nil && mdl.ContextWindow > 0 {
		cap := int(float64(mdl.ContextWindow) * maxRatio)
		if cap < defaultBudget {
			return cap
		}
	}
	return defaultBudget
}
