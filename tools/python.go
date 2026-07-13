// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/go-python/gpython/py"
	_ "github.com/go-python/gpython/stdlib"

	"github.com/pijalu/goa/internal/agentic"
)

// PythonTool executes Python code in an embedded gpython interpreter.
// Each invocation runs in an isolated gpython Context with stdout and
// stderr captured and returned to the caller.
type PythonTool struct {
	agentic.BaseTool
	// TimeoutSeconds caps the foreground execution time. Zero defaults to
	// DefaultPythonTimeoutS.
	TimeoutSeconds int
	// MaxOutputBytes caps the byte size of returned output. Zero defaults to
	// DefaultMaxBytes.
	MaxOutputBytes int
	// MaxOutputLines caps the number of lines returned. Zero defaults to
	// DefaultMaxLines.
	MaxOutputLines int
}

const (
	// DefaultPythonTimeoutS is the foreground timeout when none is specified.
	DefaultPythonTimeoutS = 60
	// MaxPythonTimeoutS is the maximum foreground timeout allowed.
	MaxPythonTimeoutS = 5 * 60
)

// pythonInput is the JSON input expected by PythonTool.
type pythonInput struct {
	Code    string `json:"code"`
	Timeout int    `json:"timeout,omitempty"`
}

// Schema returns the tool schema for python.
func (t *PythonTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "python",
		Description: "Execute Python code in an embedded gpython interpreter.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": map[string]any{
					"type":        "string",
					"description": "Python code to execute",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("timeout in seconds (default: %d, max: %d)", DefaultPythonTimeoutS, MaxPythonTimeoutS),
				},
			},
			"required": []string{"code"},
		},
	}
}

// Execute runs the Python code with a default background context.
func (t *PythonTool) Execute(input string) (string, error) {
	return t.ExecuteContext(context.Background(), input)
}

// ExecuteContext runs the Python code and forwards the caller's context so a
// cancelled turn can stop waiting for a hung interpreter.
func (t *PythonTool) ExecuteContext(ctx context.Context, input string) (string, error) {
	var p pythonInput
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", toolErr("python", "invalid_input", fmt.Sprintf("Cannot parse parameters: %v", err))
	}
	if p.Code == "" {
		return "", toolErr("python", "missing_code", "No code provided")
	}

	select {
	case <-ctx.Done():
		return "", toolErr("python", "cancelled", fmt.Sprintf("Execution cancelled: %v", ctx.Err()))
	default:
	}

	out, err := t.runPython(ctx, p.Code, normalizePythonTimeout(p.Timeout, t.TimeoutSeconds))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", toolErr("python", "cancelled", fmt.Sprintf("Execution cancelled: %v", ctxErr))
		}
		return "", err
	}

	return t.truncateOutput(out), nil
}

// runPython creates a fresh gpython context, captures stdout/stderr, and runs
// the provided code. The interpreter runs in a dedicated goroutine so a
// timeout or context cancellation can return promptly even though gpython does
// not support cooperative cancellation.
func (t *PythonTool) runPython(ctx context.Context, code string, timeout int) (string, error) {
	// Capture stdout/stderr through an os.Pipe. A pipe is used instead of a
	// bytes.Buffer because gpython's py.File wraps *os.File.
	r, w, err := os.Pipe()
	if err != nil {
		return "", toolErr("python", "io_error", fmt.Sprintf("Cannot create output pipe: %v", err))
	}
	defer r.Close()

	pyCtx := py.NewContext(py.DefaultContextOpts())
	sys := pyCtx.Store().MustGetModule("sys")
	sys.Globals["stdout"] = &py.File{File: w, FileMode: py.FileWrite}
	sys.Globals["stderr"] = &py.File{File: w, FileMode: py.FileWrite}

	var out bytes.Buffer
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		_, _ = io.Copy(&out, r)
	}()

	runDone := make(chan error, 1)
	go func() {
		_, runErr := py.RunSrc(pyCtx, code, "<python>", nil)
		pyCtx.Close()
		_ = w.Close()
		runDone <- runErr
	}()

	select {
	case err := <-runDone:
		<-readDone
		if err != nil {
			return out.String(), toolErr("python", "execution_error", formatPythonError(err))
		}
		return out.String(), nil
	case <-ctx.Done():
		return "", toolErr("python", "cancelled", fmt.Sprintf("Execution cancelled: %v", ctx.Err()))
	case <-time.After(time.Duration(timeout) * time.Second):
		return "", toolErr("python", "timeout", fmt.Sprintf("Execution timed out after %ds", timeout))
	}
}

// truncateOutput applies configured line/byte limits.
func (t *PythonTool) truncateOutput(output string) string {
	maxLines := t.MaxOutputLines
	if maxLines <= 0 {
		maxLines = DefaultMaxLines
	}
	maxBytes := t.MaxOutputBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	res := TruncateTail(output, maxLines, maxBytes)
	if res.Truncated {
		path, saveErr := SaveTruncatedOutput(output)
		if saveErr == nil {
			return fmt.Sprintf("Output truncated: %s\nFull output saved to: %s\n%s", TruncResString(res), path, res.Content)
		}
		return fmt.Sprintf("Output truncated: %s\n%s", TruncResString(res), res.Content)
	}
	return res.Content
}

// formatPythonError converts a gpython error into a readable string with a
// traceback when available.
func formatPythonError(err error) string {
	if exc, ok := err.(py.ExceptionInfo); ok {
		return exceptionInfoString(exc)
	}
	if exc, ok := err.(*py.ExceptionInfo); ok && exc != nil {
		return exceptionInfoString(*exc)
	}
	return err.Error()
}

func exceptionInfoString(exc py.ExceptionInfo) string {
	var b strings.Builder
	b.WriteString("Traceback (most recent call last):\n")
	if exc.Traceback != nil {
		exc.Traceback.TracebackDump(&b)
	}
	fmt.Fprintf(&b, "%v\n", exc.Value)
	return b.String()
}

// normalizePythonTimeout applies defaults and caps.
func normalizePythonTimeout(timeout, fallback int) int {
	if timeout <= 0 {
		timeout = fallback
	}
	if timeout <= 0 {
		timeout = DefaultPythonTimeoutS
	}
	if timeout > MaxPythonTimeoutS {
		timeout = MaxPythonTimeoutS
	}
	return timeout
}

// Access returns the tool access category for python executions.
func (t *PythonTool) Access(input string) ToolAccess {
	return ToolAccess{Category: "shell"}
}

//go:embed python.short.md python.long.md
var pythonDocs embed.FS

// ShortDoc returns the short documentation for python.
func (t *PythonTool) ShortDoc() string { return readDoc(pythonDocs, "python.short.md") }

// LongDoc returns the long documentation for python.
func (t *PythonTool) LongDoc() string { return readDoc(pythonDocs, "python.long.md") }

// Examples returns example python tool invocations.
func (t *PythonTool) Examples() []string {
	return []string{
		`{"code": "print('hello')"}`,
		`{"code": "print(sum(range(10)))"}`,
	}
}

// LoopHints supplies tool-loop-controller metadata for the python tool.
func (t *PythonTool) LoopHints() agentic.ToolLoopHints {
	return agentic.ToolLoopHints{HealArg: "code", Status: pythonRunStatus}
}

// Ensure PythonTool implements the required interfaces.
var (
	_ agentic.Tool          = (*PythonTool)(nil)
	_ agentic.ContextTool   = (*PythonTool)(nil)
	_ agentic.LoopAnnotated = (*PythonTool)(nil)
	_ Documentable          = (*PythonTool)(nil)
	_ Accessor                = (*PythonTool)(nil)
)

// pythonRunStatus returns a short status line for the TUI while the python
// tool is in flight.
func pythonRunStatus(arguments string) string {
	var p pythonInput
	if err := json.Unmarshal([]byte(arguments), &p); err != nil || p.Code == "" {
		return "Running python..."
	}
	line := strings.Split(p.Code, "\n")[0]
	if len(line) > 60 {
		line = line[:57] + "..."
	}
	return "Running: " + line
}
