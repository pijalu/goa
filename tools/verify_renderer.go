// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/tuirender"
	"github.com/pijalu/goa/internal/verify"
)

// VerifyRenderer renders verify (test-suite) tool calls and results. The call
// header shows the EXACT command verify will run plus the timeout, so the user
// can see "go test -race ./internal/app/... (timeout 30s)" instead of an
// opaque "verify". It resolves the command the same way the tool does
// (verify.ResolveFramework), so the display never lies about what executes.
type VerifyRenderer struct {
	projectDir string
}

var _ tuirender.ToolRenderer = (*VerifyRenderer)(nil)

// NewVerifyRenderer returns a renderer with no project directory set; call
// SetProjectDir at startup so auto-detected frameworks display correctly.
func NewVerifyRenderer() *VerifyRenderer { return &VerifyRenderer{} }

// SetProjectDir configures the directory used to auto-detect the test
// framework for display when the caller omits an explicit command.
func (r *VerifyRenderer) SetProjectDir(dir string) { r.projectDir = dir }

// RenderCall returns the styled command line + timeout, e.g.
// "$ go test -race ./internal/app/... (timeout 30s)".
func (r *VerifyRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	command := stringArg(args, "command")
	extra := stringSliceArg(args, "args")
	timeout := intArg(args, "timeout_seconds")

	line := r.displayLine(command, extra)
	out := rBashPrompt("$ ") + rToolOutput(line)
	if timeout > 0 {
		out += rMuted(fmt.Sprintf(" (timeout %ds)", timeout))
	} else {
		out += rMuted(" (timeout 60s)")
	}
	return out
}

// displayLine resolves the framework + args the way the tool does and returns
// the human-readable command line. It falls back to a joined raw command when
// the framework cannot be resolved (e.g. an arbitrary command).
func (r *VerifyRenderer) displayLine(command string, extra []string) string {
	framework, resolvedExtra, err := verify.ResolveFramework(r.projectDir, command, extra)
	if err == nil && framework != "" {
		if line := verify.DisplayCommandLine(framework, resolvedExtra); line != "" {
			return line
		}
	}
	if command == "" {
		return "test suite"
	}
	return joinNonEmpty(append([]string{command}, extra...), " ")
}

// RenderResult shows a compact tail of the test output (the summary line is
// already part of the report). When collapsed it shows the last few lines;
// expanded shows everything.
func (r *VerifyRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	if output == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	maxLines := 8
	if ctx.Expanded {
		maxLines = len(lines)
	}
	var b strings.Builder
	if hidden := len(lines) - maxLines; hidden > 0 {
		b.WriteString(rMuted(fmt.Sprintf("… %d earlier lines", hidden)))
		b.WriteByte('\n')
	}
	start := len(lines) - maxLines
	if start < 0 {
		start = 0
	}
	for _, line := range lines[start:] {
		if line == "" {
			continue
		}
		b.WriteString(r.styleResultLine(line))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// styleResultLine colors the leading pass/fail marker on `go test` package
// result lines (ok/fail/?) so pass/fail is legible at a glance.
func (r *VerifyRenderer) styleResultLine(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	switch {
	case strings.HasPrefix(trimmed, "ok  \t"), strings.HasPrefix(trimmed, "ok\t"):
		return rDiffAdded(line)
	case strings.HasPrefix(trimmed, "FAIL"):
		return rError(line)
	}
	return rToolOutput(line)
}

func (r *VerifyRenderer) PreviewLines() int             { return 8 }
func (r *VerifyRenderer) HideResultWhenCollapsed() bool { return false }

// stringSliceArg returns the []string value of args[key] (JSON arrays arrive
// as []any).
func stringSliceArg(args map[string]any, key string) []string {
	v, ok := args[key]
	if !ok {
		return nil
	}
	switch val := v.(type) {
	case []string:
		return val
	case []any:
		out := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func joinNonEmpty(parts []string, sep string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
