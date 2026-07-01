// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/memory"
)

// DreamResult captures the outcome of a dream run.
type DreamResult struct {
	OutputPath    string
	Consolidated  bool
	InputMemories int
	InputSessions int
	Changed       bool
}

// ProviderResolver resolves a model and builds streaming options.
type ProviderResolver interface {
	ResolveActiveModel() (provider.Model, error)
	BuildStreamOptions() provider.StreamOptions
}

// MemoryReader provides the memory operations required by DreamEngine.
type MemoryReader interface {
	List() ([]memory.MemoryFileInfo, error)
	Read(name string) (string, error)
}

// DreamEngine consolidates an existing memory store into a single, cleaned
// memory file. The original store is never modified.
type DreamEngine struct {
	cfg          *config.Config
	providerMgr  ProviderResolver
	memStore     MemoryReader
	sessionStore *SessionStore
	projectDir   string
	skillBody    string
}

// NewDreamEngine creates a DreamEngine from subsystems.
func NewDreamEngine(
	cfg *config.Config,
	providerMgr ProviderResolver,
	memStore MemoryReader,
	sessionStore *SessionStore,
	projectDir string,
	skillBody string,
) *DreamEngine {
	return &DreamEngine{
		cfg:          cfg,
		providerMgr:  providerMgr,
		memStore:     memStore,
		sessionStore: sessionStore,
		projectDir:   projectDir,
		skillBody:    skillBody,
	}
}

// Run executes the dream and writes the consolidated memory to the output
// directory. Returns the result and any error.
func (d *DreamEngine) Run(ctx context.Context, autoApply bool) (*DreamResult, error) {
	if d.memStore == nil {
		return nil, fmt.Errorf("memory store is not available")
	}

	memories, err := d.collectMemories()
	if err != nil {
		return nil, fmt.Errorf("collect memories: %w", err)
	}
	if len(memories) == 0 {
		return &DreamResult{Changed: false}, nil
	}

	sessions, err := d.collectSessions()
	if err != nil {
		return nil, fmt.Errorf("collect sessions: %w", err)
	}

	model, opts, err := d.resolveModel()
	if err != nil {
		return nil, fmt.Errorf("resolve dream model: %w", err)
	}

	prompt := d.buildDreamPrompt(memories, sessions)

	tmpFile, err := os.CreateTemp("", "goa-dream-*.md")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if err := d.streamDream(ctx, model, opts, prompt, tmpFile); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("dream stream: %w", err)
	}
	tmpFile.Close()

	outputDir := d.outputDir()
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	timestamp := time.Now().UTC().Format("20060102T150405Z")
	outputPath := filepath.Join(outputDir, timestamp+"-dream.md")
	if err := copyFile(tmpFile.Name(), outputPath); err != nil {
		return nil, fmt.Errorf("copy dream output: %w", err)
	}

	result := &DreamResult{
		OutputPath:    outputPath,
		InputMemories: len(memories),
		InputSessions: len(sessions),
		Changed:       true,
	}

	if autoApply {
		if err := d.Apply(outputPath); err != nil {
			return result, fmt.Errorf("auto-apply failed: %w", err)
		}
		result.Consolidated = true
	}

	return result, nil
}

// Apply copies a dream output into the consolidated memory location and
// backs up the original memory files.
func (d *DreamEngine) Apply(outputPath string) error {
	consolidatedDir := d.consolidatedDir()
	if err := os.MkdirAll(consolidatedDir, 0755); err != nil {
		return fmt.Errorf("create consolidated dir: %w", err)
	}

	backupDir := filepath.Join(d.projectDir, ".goa", "memory.backup", time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}

	memories, err := d.memStore.List()
	if err != nil {
		return fmt.Errorf("list memories for backup: %w", err)
	}
	for _, m := range memories {
		name := strings.TrimSuffix(m.Name, " (global)")
		content, err := d.memStore.Read(name)
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(backupDir, name+".md"), []byte(content), 0644); err != nil {
			return fmt.Errorf("backup memory %q: %w", name, err)
		}
	}

	consolidatedPath := filepath.Join(consolidatedDir, "consolidated.md")
	if err := copyFile(outputPath, consolidatedPath); err != nil {
		return fmt.Errorf("copy consolidated memory: %w", err)
	}

	return nil
}

func (d *DreamEngine) collectMemories() ([]memoryFile, error) {
	infos, err := d.memStore.List()
	if err != nil {
		return nil, err
	}

	var out []memoryFile
	for _, info := range infos {
		name := strings.TrimSuffix(info.Name, " (global)")
		content, err := d.memStore.Read(name)
		if err != nil {
			continue
		}
		out = append(out, memoryFile{Name: info.Name, Content: content})
	}
	return out, nil
}

func (d *DreamEngine) collectSessions() ([]string, error) {
	if d.sessionStore == nil {
		return nil, nil
	}
	sessions, err := d.sessionStore.ListSessions()
	if err != nil {
		return nil, err
	}

	limit := d.cfg.Memory.Dream.MinSessions
	if limit <= 0 {
		limit = 5
	}
	if len(sessions) > limit {
		sessions = sessions[:limit]
	}

	var out []string
	for _, s := range sessions {
		events, err := d.sessionStore.LoadSession(s.Name)
		if err != nil {
			continue
		}
		var text strings.Builder
		for _, ev := range events {
			if ev.Type == agentic.EventContent && ev.Text != "" {
				text.WriteString(ev.Text)
				text.WriteString("\n")
			}
		}
		if text.Len() > 0 {
			out = append(out, text.String())
		}
	}
	return out, nil
}

func (d *DreamEngine) buildDreamPrompt(memories []memoryFile, sessions []string) string {
	var b strings.Builder
	if d.skillBody != "" {
		b.WriteString(d.skillBody)
	} else {
		b.WriteString(defaultDreamSkillBody)
	}

	b.WriteString("\n\n## Input Memories\n\n")
	for _, m := range memories {
		b.WriteString(fmt.Sprintf("### %s\n\n%s\n\n", m.Name, m.Content))
	}

	if len(sessions) > 0 {
		b.WriteString("\n## Recent Session Transcripts\n\n")
		for i, s := range sessions {
			b.WriteString(fmt.Sprintf("### Session %d\n\n%s\n\n", i+1, s))
		}
	}

	return b.String()
}

const defaultDreamSkillBody = `You are a memory curator. Read the input memories and recent session transcripts below and produce a single consolidated memory file.

Rules:
- Merge duplicates; keep the most recent or most authoritative version when facts conflict.
- Remove stale or irrelevant entries.
- Surface new insights as separate H2 sections when supported by evidence.
- Preserve specific facts, file paths, command examples, and decisions.
- Do not invent facts not present in the input.
- Use concise bullet points under descriptive H2 topic headings.

Output format:

---
generated: <ISO8601 timestamp>
dream_model: <model name>
input_memories: <count>
input_sessions: <count>
---

# Consolidated Memory

## <Topic 1>

- <fact 1>
- <fact 2>
`

func (d *DreamEngine) resolveModel() (provider.Model, provider.StreamOptions, error) {
	dreamCfg := d.cfg.Memory.Dream

	providerID := dreamCfg.Provider
	modelID := dreamCfg.Model
	if providerID == "" || modelID == "" {
		providerID = d.cfg.ActiveProvider
		modelID = d.cfg.ActiveModel
	}

	if providerID == "" || modelID == "" {
		return provider.Model{}, provider.StreamOptions{}, fmt.Errorf("no dream model configured")
	}

	// Build a provider config override if needed.
	if dreamCfg.Provider != "" {
		if p := d.cfg.GetProviderByID(dreamCfg.Provider); p != nil {
			d.cfg.ActiveProvider = dreamCfg.Provider
		}
	}
	if dreamCfg.Model != "" {
		d.cfg.ActiveModel = dreamCfg.Model
	}

	model, err := d.providerMgr.ResolveActiveModel()
	if err != nil {
		return provider.Model{}, provider.StreamOptions{}, err
	}

	opts := d.providerMgr.BuildStreamOptions()
	if dreamCfg.MaxTokens > 0 {
		opts.MaxTokens = dreamCfg.MaxTokens
	}
	if dreamCfg.Temperature != 0 {
		opts.Temperature = &dreamCfg.Temperature
	}

	return model, opts, nil
}

func (d *DreamEngine) outputDir() string {
	if d.cfg.Memory.Dream.OutputDir != "" {
		return filepath.Join(d.projectDir, d.cfg.Memory.Dream.OutputDir)
	}
	return filepath.Join(d.projectDir, ".goa", "memory.dream")
}

func (d *DreamEngine) consolidatedDir() string {
	if d.cfg.Memory.Dream.ConsolidatedDir != "" {
		return filepath.Join(d.projectDir, d.cfg.Memory.Dream.ConsolidatedDir)
	}
	return filepath.Join(d.projectDir, ".goa", "memory.consolidated")
}

func (d *DreamEngine) streamDream(ctx context.Context, model provider.Model, opts provider.StreamOptions, prompt string, out io.Writer) error {
	pCtx := provider.Context{
		Context:      ctx,
		SystemPrompt: "",
		Messages: []provider.Message{
			provider.NewUserMessage(prompt),
		},
	}

	stream, err := provider.Stream(model, pCtx, opts)
	if err != nil {
		return err
	}
	defer stream.Cancel()

	return copyStreamToWriter(stream, out)
}

func copyStreamToWriter(stream *provider.AssistantMessageEventStream, out io.Writer) error {
	for event := range stream.Seq() {
		if err := copyEventToWriter(event, out); err != nil {
			return err
		}
	}
	if err := stream.Err(); err != nil {
		return err
	}
	return nil
}

func copyEventToWriter(event provider.AssistantMessageEvent, out io.Writer) error {
	switch event.Type {
	case provider.EventTextDelta:
		if _, err := io.WriteString(out, event.Delta); err != nil {
			return err
		}
	case provider.EventTextEnd:
		if event.Content != "" {
			if _, err := io.WriteString(out, event.Content); err != nil {
				return err
			}
		}
	case provider.EventError:
		if event.Error != nil {
			return event.Error
		}
	}
	return nil
}

type memoryFile struct {
	Name    string
	Content string
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
