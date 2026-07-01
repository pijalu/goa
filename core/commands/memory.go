// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/memory"
)

var _ = fmt.Errorf

// MemoryCommand manages persistent memory files.
type MemoryCommand struct{}

func (c *MemoryCommand) Name() string { return "memory" }

func memorySubcommandCompletions(prefix string) []core.ArgCompletion {
	var comps []core.ArgCompletion
	for _, v := range []struct{ val, desc string }{
		{"show", "show memory file(s)"},
		{"edit", "edit memory file"},
		{"clear", "clear memory file"},
	} {
		if prefix == "" || strings.HasPrefix(v.val, prefix) {
			comps = append(comps, core.ArgCompletion{Value: v.val, Description: v.desc})
		}
	}
	return comps
}

func memoryFileCompletions(store core.MemoryStore, sub, prefix string) []core.ArgCompletion {
	switch sub {
	case "show", "edit", "clear":
	default:
		return nil
	}

	files := []memory.MemoryFileInfo{
		{Name: "context.md"},
		{Name: "decisions.md"},
		{Name: "todos.md"},
		{Name: "notes.md"},
	}
	if store != nil {
		if listed, err := store.List(); err == nil && len(listed) > 0 {
			files = listed
		}
	}

	var comps []core.ArgCompletion
	for _, f := range files {
		name := f.Name
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}
		desc := "memory file"
		if f.Preview != "" {
			desc = f.Preview
		}
		comps = append(comps, core.ArgCompletion{Value: name, Description: desc})
	}
	return comps
}

func (c *MemoryCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	parts := strings.Split(prefix, ":")

	// Level 1: propose subcommands.
	if len(parts) == 1 && !strings.HasSuffix(prefix, ":") {
		return memorySubcommandCompletions(parts[0])
	}

	// Level 2+: complete memory file names for the subcommand.
	sub := parts[0]
	filePrefix := ""
	if len(parts) > 1 {
		filePrefix = parts[len(parts)-1]
	}
	return memoryFileCompletions(ctx.MemoryStore, sub, filePrefix)
}

func (c *MemoryCommand) Aliases() []string { return []string{} }
func (c *MemoryCommand) ShortHelp() string { return "Manage persistent memory files" }
func (c *MemoryCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *MemoryCommand) Run(ctx core.Context, args []string) error {
	args = expandCommandParams(args)

	if len(args) == 0 {
		return listMemoryFiles(ctx.MemoryStore, ctx)
	}

	switch args[0] {
	case "show":
		return showMemoryFile(ctx.MemoryStore, ctx, args)
	case "edit":
		return editMemoryFile(ctx.MemoryStore, ctx, args)
	case "clear":
		return clearMemoryFile(ctx.MemoryStore, ctx, args)
	default:
		return fmt.Errorf("unknown memory subcommand: %s (use show, edit, or clear)", args[0])
	}
}

// listMemoryFiles lists memory files using only the store and output writer.
// It demonstrates the ARCH-3 pattern: helpers depend on the narrowest
// interfaces they need rather than the full Context.
func listMemoryFiles(store core.MemoryStore, w core.OutputWriter) error {
	if store == nil {
		writeStr(w, "Memory files:\n")
		writeStr(w, "  context.md   — project context and goals\n")
		writeStr(w, "  decisions.md — architecture decisions (ADRs)\n")
		writeStr(w, "  todos.md     — task tracking\n")
		writeStr(w, "  notes.md     — general notes\n")
		return nil
	}

	files, err := store.List()
	if err != nil {
		writeFmt(w, "Error listing memory files: %v\n", err)
		return nil
	}

	if len(files) == 0 {
		writeStr(w, "No memory files found. Use /memory:edit:name to create one.\n")
		return nil
	}

	writeStr(w, "Memory files:\n")
	for _, f := range files {
		preview := ""
		if f.Preview != "" {
			preview = " — " + f.Preview
		}
		writeFmt(w, "  %s%s\n", f.Name, preview)
	}
	return nil
}

func showMemoryFile(store core.MemoryStore, w core.OutputWriter, args []string) error {
	if len(args) < 2 {
		return listMemoryFiles(store, w)
	}

	names := args[1:]
	if store == nil {
		for _, name := range names {
			writeFmt(w, "Memory '%s' — memory store not available.\n", name)
		}
		return nil
	}

	for i, name := range names {
		if i > 0 {
			writeStr(w, "\n")
		}
		writeFmt(w, "Memory '%s' content:\n", name)
		content, err := store.Read(name)
		if err != nil {
			writeFmt(w, "Error: %v\n", err)
			continue
		}
		writeStr(w, content+"\n")
	}
	return nil
}

func editMemoryFile(store core.MemoryStore, w core.OutputWriter, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: /memory:edit <name>")
	}
	if store == nil {
		writeFmt(w, "Edit memory '%s' — memory store not available\n", args[1])
		return nil
	}
	name := args[1]
	content, err := store.Read(name)
	if err != nil {
		content = fmt.Sprintf("# %s\n\nCreated: %s\n\n", name, time.Now().Format(time.RFC3339))
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}

	tmpFile, err := os.CreateTemp("", "goa-memory-*.md")
	if err != nil {
		writeFmt(w, "Error creating temp file: %v\n", err)
		return nil
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		writeFmt(w, "Error writing temp file: %v\n", err)
		return nil
	}
	tmpFile.Close()

	cmd := exec.Command(editor, tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		writeFmt(w, "Editor error: %v\n", err)
		return nil
	}

	edited, err := os.ReadFile(tmpPath)
	if err != nil {
		writeFmt(w, "Error reading edited file: %v\n", err)
		return nil
	}

	if err := store.Write(name, string(edited)); err != nil {
		writeFmt(w, "Error saving memory: %v\n", err)
		return nil
	}

	writeFmt(w, "Saved memory '%s' (%d bytes)\n", name, len(edited))
	return nil
}

func clearMemoryFile(store core.MemoryStore, w core.OutputWriter, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: /memory:clear <name>")
	}
	if store == nil {
		writeFmt(w, "Clear memory '%s' — memory store not available\n", args[1])
		return nil
	}
	if err := store.Delete(args[1]); err != nil {
		writeFmt(w, "Error clearing memory '%s': %v\n", args[1], err)
		return nil
	}
	writeFmt(w, "Cleared memory '%s'\n", args[1])
	return nil
}
