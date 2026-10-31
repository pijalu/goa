// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pijalu/goa/config"
)

// runCleanupCLI handles `goa cleanup` before flag parsing, subsystem creation,
// or TUI startup. The console wizard is the entire process for this invocation.
func runCleanupCLI(args []string) bool {
	if len(args) == 0 || args[0] != "cleanup" {
		return false
	}
	if len(args) != 1 {
		fatalExitf("goa cleanup: no arguments expected\n")
	}
	if err := runCleanupConsole(MustGetwd(), os.Stdin, os.Stdout); err != nil {
		fatalExitf("goa cleanup: %v\n", err)
	}
	return true
}

func runCleanupConsole(projectDir string, input io.Reader, output io.Writer) error {
	loader := config.NewCascadeLoader(projectDir, "", nil)
	plan, err := loader.PlanProjectCleanup()
	if err != nil {
		return err
	}
	fmt.Fprintln(output, "Comparing project configuration with home configuration; values stay hidden.")
	if len(plan.Candidates) == 0 {
		fmt.Fprintln(output, "No redundant or conflicting project configuration found.")
		return nil
	}

	reader := bufio.NewReader(input)
	approved := make([]string, 0, len(plan.Candidates))
	for _, candidate := range plan.Candidates {
		printCleanupCandidate(output, candidate)
		accept, proceed, err := confirmCleanupCandidate(reader, output)
		if err != nil {
			return fmt.Errorf("read confirmation: %w", err)
		}
		if !proceed {
			fmt.Fprintln(output, "\nCleanup cancelled; configuration unchanged.")
			return nil
		}
		if accept {
			approved = append(approved, candidate.Key)
		}
	}
	removed, err := plan.Apply(approved)
	if err != nil {
		return err
	}
	noun := "items"
	if removed == 1 {
		noun = "item"
	}
	fmt.Fprintf(output, "Removed %d project %s; kept %d.\n", removed, noun, len(plan.Candidates)-removed)
	return nil
}

func printCleanupCandidate(output io.Writer, candidate config.CleanupCandidate) {
	if candidate.Kind == config.CleanupRedundant {
		fmt.Fprintf(output, "Redundant project %s %q duplicates the home entry.\n", candidate.Type, candidate.Name)
	} else {
		fmt.Fprintf(output, "Conflicting project %s %q differs from the home entry.\n", candidate.Type, candidate.Name)
	}
	if len(candidate.Fields) != 0 {
		fmt.Fprintf(output, "Differing fields: %s (values hidden).\n", strings.Join(candidate.Fields, ", "))
	}
}

func confirmCleanupCandidate(reader *bufio.Reader, output io.Writer) (accepted, proceed bool, err error) {
	for {
		fmt.Fprint(output, "Remove this project item and inherit from home? (y/n) [n]: ")
		answer, readErr := reader.ReadString('\n')
		if errors.Is(readErr, io.EOF) {
			return false, false, nil
		}
		if readErr != nil {
			return false, false, readErr
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "y", "yes":
			return true, true, nil
		case "", "n", "no":
			return false, true, nil
		default:
			fmt.Fprintln(output, "Please answer y or n.")
		}
	}
}

const cleanupCLIUsage = `goa cleanup — reconcile project configuration with home configuration

Usage:
   goa cleanup

Compares only ~/.goa/config.yaml and the current project's .goa/config.yaml.
Offers to remove duplicate active settings, same-ID provider/model entries,
and other shared top-level sections. Conflicting entries are preserved unless
explicitly approved for removal. Project-only top-level sections, local
overrides, environment values, CLI overrides, and home configuration are never changed.
Credential values are never displayed.

Every proposed removal has its own (y/n) prompt; the default answer is no.
EOF or cancellation leaves the project file byte-for-byte unchanged. Approved
changes are written together only after every prompt has been answered.
`
