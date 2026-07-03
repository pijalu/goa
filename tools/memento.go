// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
)

// namePattern validates memory file names.
var namePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// MementoTool provides read/write/append/list/delete access to memory files
// stored in .goa/memory/ (project) and ~/.goa/memory/ (global).
type MementoTool struct {
	ProjectDir string
	GlobalDir  string
}

// Schema returns the tool schema for memento.
func (t *MementoTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "memento",
		Description: "Persistent memory files.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string", "description": "Action to perform",
					"enum": []string{"read", "write", "append", "list", "delete"},
				},
				"name": map[string]any{
					"type": "string", "description": "Memory file name (alphanumeric + hyphens only)",
				},
				"content": map[string]any{
					"type": "string", "description": "Content for write/append actions",
				},
				"section": map[string]any{
					"type": "string", "description": "Section heading for append action (e.g., 'decisions')",
				},
			},
			"required": []string{"action"},
		},
	}
}

// mementoParams holds the parsed input for MementoTool.
type mementoParams struct {
	Action  string `json:"action"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Section string `json:"section"`
}

// Execute runs the requested memory action.
func (t *MementoTool) Execute(input string) (string, error) {
	var p mementoParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", &internal.ToolError{
			Tool: "memento", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot parse parameters: %v", err),
			HintText: "Ensure your input is valid JSON with the required fields.",
		}
	}

	switch p.Action {
	case "list":
		return t.listMemory()
	case "read":
		return t.readMemory(p.Name)
	case "write":
		return t.writeMemory(p.Name, p.Content)
	case "append":
		return t.appendMemory(p.Name, p.Section, p.Content)
	case "delete":
		return t.deleteMemory(p.Name)
	default:
		return "", &internal.ToolError{
			Tool: "memento", Type: "unknown_action",
			Detail:   fmt.Sprintf("Unknown action: %s", p.Action),
			HintText: "Use one of: read, write, append, list, delete",
		}
	}
}

func (t *MementoTool) IsRetryable(err error) bool { return false }

//go:embed memento.short.md memento.long.md
var mementoDocs embed.FS

func (t *MementoTool) ShortDoc() string { return readDoc(mementoDocs, "memento.short.md") }
func (t *MementoTool) LongDoc() string  { return readDoc(mementoDocs, "memento.long.md") }

func (t *MementoTool) Examples() []string {
	return []string{
		`{"action": "list"}`,
		`{"action": "read", "name": "context"}`,
		`{"action": "write", "name": "todos", "content": "- [ ] Finish auth module"}`,
		`{"action": "append", "name": "decisions", "section": "architecture", "content": "Use PostgreSQL for persistence"}`,
		`{"action": "delete", "name": "old-notes"}`,
	}
}

func (t *MementoTool) projectMemoryDir() string {
	return filepath.Join(t.ProjectDir, ".goa", "memory")
}

func (t *MementoTool) globalMemoryDir() string {
	if t.GlobalDir != "" {
		return filepath.Join(t.GlobalDir, "memory")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".goa", "memory")
}

func (t *MementoTool) validateName(name string) error {
	if !namePattern.MatchString(name) {
		return &internal.ToolError{
			Tool: "memento", Type: "invalid_name",
			Detail:   fmt.Sprintf("Invalid memory name %q: use lowercase alphanumeric and hyphens only", name),
			HintText: "Name must match ^[a-z0-9-]+$",
		}
	}
	return nil
}

type memFileInfo struct {
	Name    string
	Preview string
	Mtime   time.Time
}

func (t *MementoTool) listMemory() (string, error) {
	projectFiles := t.scanDir(t.projectMemoryDir(), false)
	globalFiles := t.scanDir(t.globalMemoryDir(), true)
	for _, gf := range globalFiles {
		duplicate := false
		for _, pf := range projectFiles {
			if pf.Name == gf.Name {
				duplicate = true
				break
			}
		}
		if !duplicate {
			projectFiles = append(projectFiles, gf)
		}
	}

	files := projectFiles
	if len(files) == 0 {
		return "[memento: list] No memory files found", nil
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Mtime.After(files[j].Mtime)
	})

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "[memento: list] %d memory file(s)\n", len(files))
	for _, f := range files {
		ago := time.Since(f.Mtime).Round(time.Second)
		fmt.Fprintf(&buf, "  %-20s — %s (%s ago)\n", f.Name, f.Preview, ago)
	}
	return buf.String(), nil
}

func (t *MementoTool) scanDir(dir string, isGlobal bool) []memFileInfo {
	var files []memFileInfo
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, _ := e.Info()
		name := strings.TrimSuffix(e.Name(), ".md")
		if isGlobal {
			name += " (global)"
		}
		preview := t.readPreview(filepath.Join(dir, e.Name()))
		files = append(files, memFileInfo{name, preview, info.ModTime()})
	}
	return files
}

func (t *MementoTool) readPreview(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "---") {
			if len(trimmed) > 80 {
				trimmed = trimmed[:80] + "..."
			}
			return trimmed
		}
	}
	return ""
}

func (t *MementoTool) readMemory(name string) (string, error) {
	if err := t.validateName(name); err != nil {
		return "", err
	}

	// Check project dir first
	projectPath := filepath.Join(t.projectMemoryDir(), name+".md")
	if data, err := os.ReadFile(projectPath); err == nil {
		return formatMemoryRead(name, string(data), "project"), nil
	}

	// Fall back to global dir
	globalPath := filepath.Join(t.globalMemoryDir(), name+".md")
	if data, err := os.ReadFile(globalPath); err == nil {
		return formatMemoryRead(name, string(data), "global"), nil
	}

	return "", &internal.ToolError{
		Tool: "memento", Type: "not_found",
		Detail:   fmt.Sprintf("Memory file %q not found", name),
		HintText: "Use list action to see available memory files, or write action to create one.",
	}
}

func formatMemoryRead(name, content, source string) string {
	return fmt.Sprintf("[memento: %s] (from %s)\n%s", name, source, content)
}

func (t *MementoTool) writeMemory(name, content string) (string, error) {
	if err := t.validateName(name); err != nil {
		return "", err
	}

	dir := t.projectMemoryDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create memory dir: %w", err)
	}

	path := filepath.Join(dir, name+".md")
	// Add frontmatter with timestamp
	frontmatter := fmt.Sprintf("---\ncreated: %s\nupdated: %s\n---\n\n",
		time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	fullContent := frontmatter + content

	if err := os.WriteFile(path, []byte(fullContent), 0644); err != nil {
		return "", fmt.Errorf("write memory: %w", err)
	}

	return fmt.Sprintf("[memento: write] %s — written to project memory", name), nil
}

func (t *MementoTool) appendMemory(name, section, content string) (string, error) {
	if err := t.validateName(name); err != nil {
		return "", err
	}

	dir := t.projectMemoryDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create memory dir: %w", err)
	}

	path := filepath.Join(dir, name+".md")
	existing := t.readOrCreateMemory(path)

	if section != "" {
		existing = t.appendToSection(existing, section, content)
	} else {
		existing += "\n" + content + "\n"
	}

	if err := os.WriteFile(path, []byte(existing), 0644); err != nil {
		return "", fmt.Errorf("append memory: %w", err)
	}

	return fmt.Sprintf("[memento: append] %s — content appended", name), nil
}

func (t *MementoTool) readOrCreateMemory(path string) string {
	if data, err := os.ReadFile(path); err == nil {
		return string(data)
	}
	return fmt.Sprintf("---\ncreated: %s\nupdated: %s\n---\n\n",
		time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
}

func (t *MementoTool) appendToSection(existing, section, content string) string {
	sectionHeader := "## " + section
	if !strings.Contains(existing, sectionHeader) {
		return existing + "\n" + sectionHeader + "\n\n" + content + "\n"
	}

	lines := strings.Split(existing, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == sectionHeader {
			end := t.findSectionEnd(lines, i+1)
			newLines := make([]string, 0, len(lines)+2)
			newLines = append(newLines, lines[:end]...)
			newLines = append(newLines, "", content)
			newLines = append(newLines, lines[end:]...)
			return strings.Join(newLines, "\n")
		}
	}
	return existing
}

func (t *MementoTool) findSectionEnd(lines []string, start int) int {
	for j := start; j < len(lines); j++ {
		if strings.HasPrefix(strings.TrimSpace(lines[j]), "##") {
			return j
		}
	}
	return len(lines)
}

func (t *MementoTool) deleteMemory(name string) (string, error) {
	if err := t.validateName(name); err != nil {
		return "", err
	}

	path := filepath.Join(t.projectMemoryDir(), name+".md")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return "", &internal.ToolError{
				Tool: "memento", Type: "not_found",
				Detail:   fmt.Sprintf("Memory file %q not found", name),
				HintText: "Use list action to see available memory files.",
			}
		}
		return "", fmt.Errorf("delete memory: %w", err)
	}

	return fmt.Sprintf("[memento: delete] %s — deleted", name), nil
}
