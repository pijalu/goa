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
	"github.com/pijalu/goa/tui"
)

// buildSystemPrompt assembles the complete system prompt from all sources:
//  1. Mode system prompt (coder/planner/reviewer)
//  2. AGENTS.md context files (wrapped in <project_context>)
//  3. Long-term memory summaries (wrapped in <memory>)
//  4. Available skills listing (wrapped in <available_skills>)
//  5. Active skill bodies from the current mode's skill stack
//  6. Self-documentation links (goa:// references for the LLM)
//  7. Agent-driven prompts (if enabled, added by agentmanager)
func buildSystemPrompt(subs *subsystems) string {
	var parts []string

	// 1. Mode system prompt (base) — read from agent runtime state, not config
	if body := modeSystemPrompt(subs); body != "" {
		parts = append(parts, body)
	}

	// 2. AGENTS.md context files
	if len(subs.contextFiles) > 0 {
		parts = append(parts, renderContextFiles(subs.contextFiles))
	}

	// 3. Long-term memory summaries
	if memSection := buildMemorySection(subs); memSection != "" {
		parts = append(parts, memSection)
	}

	// 4. Available skills (all skills listed regardless of inline vs sub-agent)
	if rendered := availableSkillsSection(subs); rendered != "" {
		parts = append(parts, rendered)
	}

	// 5. Active skill bodies from the current mode's skill stack.
	// These are loaded before the conversation starts (e.g. /telegram) and
	// become part of the system instructions rather than a user message.
	if activeSkills := buildActiveSkillsSection(subs); activeSkills != "" {
		parts = append(parts, activeSkills)
	}

	// 6. Self-documentation links (single source of truth — generated from embedded docs)
	if selfDoc := buildSelfDocSection(); selfDoc != "" {
		parts = append(parts, selfDoc)
	}

	return strings.Join(parts, "\n\n")
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
	return skills.RenderAvailableSkills(subs.promptReg, skillSummaries)
}

// buildSelfDocSection returns a standalone <goa_documentation> section that
// lists every embedded document and how to read it via the goa:// scheme.
// Because it is generated from the docs package, it stays up to date when
// documents are added, removed, or renamed.
func buildSelfDocSection() string {
	docList, err := docs.List()
	if err != nil || len(docList) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<goa_documentation>\n")
	b.WriteString("Goa embeds its own reference documentation. Read any document with the read tool using a goa:// URL.\n\n")
	b.WriteString("How to use Goa documentation:\n")
	b.WriteString("- To create or use user skills: read goa://docs/SKILLS.md\n")
	b.WriteString("- To build or load plugins: read goa://docs/PLUGINS.md\n")
	b.WriteString("- To configure Goa (profiles, providers, settings): read goa://docs/CONFIGURATION.md\n")
	b.WriteString("- To call tools and understand tool schemas: read goa://docs/TOOLS.md\n")
	b.WriteString("- To use slash commands and the command system: read goa://docs/COMMANDS.md\n\n")
	b.WriteString("All available reference documents:\n")
	for _, d := range docList {
		fmt.Fprintf(&b, "- %s: read goa://%s (%s)\n", d.Name, d.Path, d.Description)
	}
	b.WriteString("\nUse /docs in the chat to list documents or /docs:name to view one.")
	b.WriteString("\n</goa_documentation>")
	return b.String()
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
		// Account for both per-skill frontmatter inline flag and the global
		// skills.execution_mode config. When global mode is "inline", skills
		// not explicitly marked inline are "forced inline" — they run inline
		// despite not declaring it themselves.
		globalModeInline := subs.cfg != nil && subs.cfg.Skills.ExecutionMode == config.AgenticSkillModeInline

		inlineCount := 0
		forcedInlineCount := 0
		subCount := 0
		for _, s := range skillList {
			if s.Inline {
				inlineCount++
			} else if globalModeInline {
				forcedInlineCount++
			} else {
				subCount++
			}
		}

		// Determine the effective global execution mode.
		globalMode := "sub-agent"
		if globalModeInline {
			globalMode = "inline"
		}

		if forcedInlineCount > 0 {
			chat.AddSystemMessage(fmt.Sprintf("⟡ %d skills (%d inline, %d forced inline · global mode: %s). Use /skill to list.",
				len(skillList), inlineCount, forcedInlineCount, globalMode))
		} else {
			chat.AddSystemMessage(fmt.Sprintf("⟡ %d skills (%d inline, %d sub-agent · global mode: %s). Use /skill to list.",
				len(skillList), inlineCount, subCount, globalMode))
		}

	} else {
		chat.AddSystemMessage("⟡ No skills loaded")
	}
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
		Profile:                subs.cfg.ActiveMajor(),
		Mode:                   string(subs.cfg.DefaultModeState().Autonomy),
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

	providerName := providerCfg.Name
	if providerName == "" {
		providerName = providerCfg.ID
	}
	msg := fmt.Sprintf("Connected to %s (%s).", providerName, model)
	chat.AddInfoMessage(msg)
	subs.tuiEngine.RequestRender()
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

// buildActiveSkillsSection constructs a <skills> block containing the full
// bodies of skills that have been loaded into the current mode's skill stack
// before the conversation started.
func buildActiveSkillsSection(subs *subsystems) string {
	if subs.agentMgr == nil || subs.skillRegistry == nil {
		return ""
	}
	mode := subs.agentMgr.CurrentMode()
	if len(mode.Skills) == 0 {
		return ""
	}

	var bodies []string
	for _, name := range mode.Skills {
		skill, ok := subs.skillRegistry.Get(name)
		if !ok {
			continue
		}
		bodies = append(bodies, fmt.Sprintf("<skill name=%q>\n%s\n</skill>", name, skill.Body))
	}
	if len(bodies) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<skills>\n")
	b.WriteString("The following skill instructions are active for this session:\n\n")
	b.WriteString(strings.Join(bodies, "\n\n"))
	b.WriteString("\n</skills>")
	return b.String()
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
	entry := fmt.Sprintf("Consolidated memory (read full file for details):\n%s", summary)
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
		entries = append(entries, fmt.Sprintf("File: %s\nSummary: %s", f.Name, ms))
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
		entries = append(entries, fmt.Sprintf("File: %s\nSummary: %s", f.Name, summary))
	}
	return entries, used
}

func renderMemorySection(entries []string) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<memory>\n")
	b.WriteString("The following are summaries of long-term memories. If a summary is relevant, read the full file with read_file.\n\n")
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
