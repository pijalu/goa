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

	"github.com/go-python/gpython/compile"
	"github.com/go-python/gpython/py"
	_ "github.com/go-python/gpython/stdlib"

	"github.com/pijalu/goa/internal/agentic"
	_ "github.com/pijalu/goa/internal/python/stdlib"
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
	// ProjectDir is the absolute path of the workspace root. When non-empty it
	// is the base the embedded `os` module resolves relative paths against.
	ProjectDir string
	// Jail, when true and ProjectDir is set, confines all `os` file-module
	// operations to ProjectDir and below, matching the bash tool's jail.
	Jail bool
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
		Description: "Execute Python code in an embedded gpython interpreter. Standard file API via os (os.walk/os.stat/os.path) is jail-confined to the project; os.system/os._exit are disabled.",
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

	// Install the jail-confined, curated `os` module (with `os.path`) so that
	// typical LLM Python file code (os.walk, os.stat, os.path.join, ...) runs
	// unmodified. It shadows the sparse stock gpython os module and omits
	// unsafe primitives (os.system, os._exit).
	if err := installGoaFsModules(pyCtx, newPyFileScope(t.ProjectDir, t.Jail)); err != nil {
		pyCtx.Close()
		_ = w.Close()
		<-readDone
		return "", toolErr("python", "setup_error", fmt.Sprintf("Cannot install os file module: %v", err))
	}

	comp, err := compilePythonCode(code)
	if err != nil {
		pyCtx.Close()
		_ = w.Close()
		<-readDone
		return "", toolErr("python", "execution_error", formatPythonError(err))
	}

	runDone := make(chan error, 1)
	go func() {
		_, runErr := py.RunCode(pyCtx, comp, "<python>", nil)
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

// compilePythonCode compiles the Python buffer in exec mode. If the code uses
// f-strings (unsupported by the embedded Python 3.4 interpreter) it applies a
// best-effort rewrite to str.format() before retrying.
func compilePythonCode(code string) (*py.Code, error) {
	comp, err := compile.Compile(code+"\n", "<python>", py.ExecMode, 0, true)
	if err == nil {
		return comp, nil
	}
	if !strings.Contains(code, "f\"") && !strings.Contains(code, "f'") {
		return nil, err
	}
	transformed := transformFStrings(code)
	comp, err = compile.Compile(transformed+"\n", "<python>", py.ExecMode, 0, true)
	if err == nil {
		return comp, nil
	}
	return nil, err
}

// transformFStrings rewrites f"..." and f'...' literals with non-nested
// braces to equivalent %-formatting calls. This is a compatibility shim for the
// embedded interpreter, which does not implement f-strings.
func transformFStrings(code string) string {
	var out strings.Builder
	i := 0
	for i < len(code) {
		if i+1 < len(code) && code[i] == 'f' && (code[i+1] == '"' || code[i+1] == '\'') {
			rewritten, next, ok := rewriteFStringAt(code, i)
			if !ok {
				return code
			}
			out.WriteString(rewritten)
			i = next
			continue
		}
		out.WriteByte(code[i])
		i++
	}
	return out.String()
}

// rewriteFStringAt parses a single f-string starting at position i (where
// code[i]=='f' and code[i+1] is the quote character). It returns the rewritten
// %-format expression, the index after the f-string, and false if the f-string
// is malformed (e.g. unclosed brace).
func rewriteFStringAt(code string, i int) (string, int, bool) {
	quote := code[i+1]
	i += 2
	template, exprs, next, ok := parseFStringContent(code, i, quote)
	if !ok {
		return "", 0, false
	}
	var out strings.Builder
	out.WriteByte(quote)
	out.WriteString(template)
	out.WriteByte(quote)
	if len(exprs) > 0 {
		out.WriteString(" % (")
		for j, e := range exprs {
			if j > 0 {
				out.WriteString(", ")
			}
			out.WriteString(e)
		}
		out.WriteByte(')')
	}
	return out.String(), next, true
}

// parseFStringContent scans the inside of an f-string starting at i and
// returns the %-format template, the expression list, the index after the
// closing quote, and a flag indicating whether the f-string was well-formed.
func parseFStringContent(code string, i int, quote byte) (string, []string, int, bool) {
	var template strings.Builder
	var exprs []string
	for i < len(code) {
		if code[i] == '\\' && i+1 < len(code) {
			// Keep escaped chars in the template as-is.
			template.WriteByte(code[i])
			template.WriteByte(code[i+1])
			i += 2
			continue
		}
		if code[i] == quote {
			i++
			break
		}
		if code[i] == '{' {
			expr, spec, next, ok := parseFStringExpr(code, i+1)
			if !ok {
				return "", nil, 0, false
			}
			if spec == "" {
				template.WriteString("%s")
			} else {
				template.WriteString("%" + spec)
			}
			exprs = append(exprs, expr)
			i = next
			continue
		}
		template.WriteByte(code[i])
		i++
	}
	return template.String(), exprs, i, true
}

// parseFStringExpr parses a single {...} expression inside an f-string. It
// returns the expression, the optional format spec, the index after the closing
// brace, and a flag indicating success.
func parseFStringExpr(code string, i int) (string, string, int, bool) {
	exprStart := i
	depth := 1
	for i < len(code) && depth > 0 {
		if code[i] == '{' {
			depth++
		} else if code[i] == '}' {
			depth--
			if depth == 0 {
				break
			}
		}
		i++
	}
	if i >= len(code) {
		return "", "", 0, false
	}
	expr := code[exprStart:i]
	var spec string
	if colon := strings.Index(expr, ":"); colon >= 0 {
		spec = expr[colon+1:]
		expr = expr[:colon]
	}
	return expr, spec, i + 1, true
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
	_ Accessor              = (*PythonTool)(nil)
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
