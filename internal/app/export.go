// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pijalu/goa/core/commands"
)

// ExportOptions holds CLI flags for `goa export`.
type ExportOptions struct {
	OutputPath       string
	SessionID        string
	IncludeGlobalLog bool
	Yes              bool
	IssueDescription string
}

// runExport executes the headless `goa export` command.
func runExport(subs *subsystems, opts ExportOptions) {
	if err := doExport(subs, opts); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func doExport(subs *subsystems, opts ExportOptions) error {
	sessionID := resolveExportSessionID(subs, opts.SessionID)
	if sessionID == "" {
		return fmt.Errorf("no session found to export")
	}

	if !opts.Yes && !opts.IncludeGlobalLog && isGlobalLogOutsideProject(subs) {
		fmt.Fprintf(os.Stderr, "Global log is outside the project directory. Use --include-global-log to bundle it.\n")
	}

	outputPath := defaultExportPath(subs.projectDir, opts.OutputPath)
	issue := opts.IssueDescription
	if issue == "" && !opts.Yes {
		var err error
		issue, err = readExportIssueDescription()
		if err != nil {
			return err
		}
	}

	ctx := coreContextForCommand(subs, nil)
	cmd := &commands.ExportSessionCommand{
		ProjectDir:  subs.projectDir,
		Issue:       issue,
		OutputPath:  outputPath,
		SessionID:   sessionID,
		IncludeLogs: opts.IncludeGlobalLog,
	}
	return cmd.Run(ctx)
}

func resolveExportSessionID(subs *subsystems, explicitID string) string {
	if explicitID != "" {
		return explicitID
	}
	if subs.sessionStore != nil {
		if id := subs.sessionStore.SessionID(); id != "" {
			return id
		}
	}
	if prev, err := findMostRecentSession(subs); err == nil {
		return prev
	}
	return ""
}

func defaultExportPath(projectDir, outputPath string) string {
	if outputPath != "" {
		return outputPath
	}
	exportDir := filepath.Join(projectDir, ".goa", "exports")
	_ = os.MkdirAll(exportDir, 0o755)
	ts := time.Now().Format("20060102-150405")
	return filepath.Join(exportDir, fmt.Sprintf("goa-export-%s.zip", ts))
}

func findMostRecentSession(subs *subsystems) (string, error) {
	if subs.sessionStore == nil {
		return "", fmt.Errorf("no session store")
	}
	sessions, err := subs.sessionStore.ListSessions()
	if err != nil {
		return "", err
	}
	if len(sessions) == 0 {
		return "", fmt.Errorf("no saved sessions")
	}
	return sessions[0].Name, nil
}

func isGlobalLogOutsideProject(subs *subsystems) bool {
	if subs.cfg == nil || subs.cfg.Logging.File == "" {
		return false
	}
	rel, err := filepath.Rel(subs.projectDir, subs.cfg.Logging.File)
	if err != nil {
		return true
	}
	return strings.HasPrefix(rel, "..")
}

func readExportIssueDescription() (string, error) {
	fmt.Fprint(os.Stderr, "Describe the issue (optional, then press Enter twice):\n")
	scanner := bufio.NewScanner(os.Stdin)
	var lines []string
	blankCount := 0
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			blankCount++
			if blankCount >= 2 {
				break
			}
			continue
		}
		blankCount = 0
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}

// exportOptionsFromRuntime derives ExportOptions from RuntimeOptions.
func exportOptionsFromRuntime(opts RuntimeOptions) ExportOptions {
	return ExportOptions{
		OutputPath:       opts.ExportOutput,
		SessionID:        opts.ExportSession,
		IncludeGlobalLog: opts.IncludeGlobalLog,
		Yes:              opts.Yes,
	}
}
