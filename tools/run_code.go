// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pijalu/gpython/py"
	_ "github.com/pijalu/gpython/stdlib"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/python/compat"
	_ "github.com/pijalu/goa/internal/python/stdlib"
	"github.com/pijalu/goa/tools/common"
)

// RunCodeTool executes a Python program in the jailed gpython interpreter with
// a `tools` binding: every attribute is a registered tool, and calling it
// dispatches through the agent's guarded tool pipeline exactly like a direct
// tool call (gap TL7, dsh code-mode parity). Each sub-call is recorded in a
// durable per-run dispatch log; oversized sub-call results are spill-capped so
// the log stays bounded.
//
// The program answers via print(): only what it prints comes back, so the
// model-facing result is curated. The embedded interpreter is Python 3.4 and
// has no async/await, so this adaptation exposes synchronous calls
// (`tools.name({...})`) instead of dsh's `await tools.name(args)`.
type RunCodeTool struct {
	agentic.BaseTool
	// TimeoutSeconds caps the foreground execution time. Zero defaults to
	// DefaultRunCodeTimeoutS.
	TimeoutSeconds int
	// MaxOutputBytes caps the byte size of returned output. Zero defaults to
	// DefaultMaxBytes.
	MaxOutputBytes int
	// MaxOutputLines caps the number of lines returned. Zero defaults to
	// DefaultMaxLines.
	MaxOutputLines int
	// MaxProgramBytes caps the submitted program length. Zero defaults to
	// DefaultRunCodeMaxProgramBytes.
	MaxProgramBytes int
	// ProjectDir is the absolute path of the workspace root. When non-empty it
	// is the base the embedded `os` module resolves relative paths against.
	ProjectDir string
	// Jail, when true and ProjectDir is set, confines all `os` file-module
	// operations to ProjectDir and below, matching the python/bash jail.
	Jail bool
	// Registry supplies the sub-call surface: every registered tool (except
	// run_code itself) becomes a `tools.<name>` binding attribute, resolved
	// fresh at each execution so dynamic tools (MCP) join too.
	Registry *ToolRegistry
	// DispatchDir is the directory for the durable per-sub-call dispatch log
	// and its spill files. Empty disables persistence.
	DispatchDir string
	// MaxLogResultBytes caps the inline sub-call result bytes stored in the
	// dispatch log. Oversized results spill to the dispatch spill dir and the
	// entry records the path. Zero defaults to DefaultRunCodeMaxLogResultBytes.
	MaxLogResultBytes int
}

const (
	// RunCodeToolName is the model-facing name of the run_code tool.
	RunCodeToolName = "run_code"
	// DefaultRunCodeTimeoutS is the foreground timeout when none is specified.
	DefaultRunCodeTimeoutS = 60
	// MaxRunCodeTimeoutS is the maximum foreground timeout allowed.
	MaxRunCodeTimeoutS = 5 * 60
	// DefaultRunCodeMaxProgramBytes caps the submitted program length.
	DefaultRunCodeMaxProgramBytes = 200 * 1024
	// DefaultRunCodeMaxLogResultBytes caps the inline result bytes per dispatch
	// log entry.
	DefaultRunCodeMaxLogResultBytes = 64 * 1024
)

// runCodeInput is the JSON input expected by RunCodeTool.
type runCodeInput struct {
	Code        string `json:"code"`
	Description string `json:"description"`
	Timeout     int    `json:"timeout,omitempty"`
}

// Schema returns the tool schema for run_code.
func (t *RunCodeTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        RunCodeToolName,
		Description: "Execute a Python program against the available tools. The program runs in the jailed embedded interpreter; call tools as `tools.name({...})` per the tool declarations in the system prompt. Multiple sub-calls are dispatched through the same guarded permission/jail path as direct tool calls, and every sub-call is recorded in a durable dispatch log. Only what the program prints comes back — curate it.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": map[string]any{
					"type":        "string",
					"description": "The program: Python code executed in the jailed embedded interpreter (Python 3.4 subset; no async/await — call tools synchronously as `tools.name({...})`). Answer via print().",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Clear, concise description of what this program does in active voice, 5-10 words (shown in the UI). Examples: \"Count TODO markers across packages\"; \"Read failing test and its fixture\".",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("timeout in seconds (default: %d, max: %d)", DefaultRunCodeTimeoutS, MaxRunCodeTimeoutS),
				},
			},
			"required": []string{"code", "description"},
		},
	}
}

// Execute runs the program with a default background context.
func (t *RunCodeTool) Execute(input string) (string, error) {
	return t.ExecuteContext(context.Background(), input)
}

// ExecuteContext runs the program and forwards the caller's context so a
// cancelled turn can stop waiting for a hung interpreter.
func (t *RunCodeTool) ExecuteContext(ctx context.Context, input string) (string, error) {
	var p runCodeInput
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", toolErr(RunCodeToolName, "invalid_input", fmt.Sprintf("Cannot parse parameters: %v", err))
	}
	if p.Code == "" {
		return "", toolErr(RunCodeToolName, "missing_code", "No code provided")
	}
	if strings.TrimSpace(p.Description) == "" {
		return "", toolErr(RunCodeToolName, "missing_description", "A non-empty description is required")
	}
	if len(p.Code) > t.maxProgramBytes() {
		return "", toolErr(RunCodeToolName, "program_too_large", fmt.Sprintf("Program is %d bytes; the maximum is %d bytes", len(p.Code), t.maxProgramBytes()))
	}

	select {
	case <-ctx.Done():
		return "", toolErr(RunCodeToolName, "cancelled", fmt.Sprintf("Execution cancelled: %v", ctx.Err()))
	default:
	}

	out, err := t.runProgram(ctx, p)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", toolErr(RunCodeToolName, "cancelled", fmt.Sprintf("Execution cancelled: %v", ctxErr))
		}
		return "", err
	}
	return t.truncateOutput(out), nil
}

// maxProgramBytes applies the default for MaxProgramBytes.
func (t *RunCodeTool) maxProgramBytes() int {
	if t.MaxProgramBytes > 0 {
		return t.MaxProgramBytes
	}
	return DefaultRunCodeMaxProgramBytes
}

// maxLogResultBytes applies the default for MaxLogResultBytes.
func (t *RunCodeTool) maxLogResultBytes() int {
	if t.MaxLogResultBytes > 0 {
		return t.MaxLogResultBytes
	}
	return DefaultRunCodeMaxLogResultBytes
}

// runCodeRun carries the per-execution state shared by the tools binding.
type runCodeRun struct {
	tool     *RunCodeTool
	ctx      context.Context
	runID    string
	seq      int
	log      *common.DispatchLog
	spill    *common.SpillStore
	start    time.Time
	toolsMod *py.Module
}

// newRunCodeRun builds the per-run state, opening the durable dispatch log and
// the spill store under DispatchDir (both best-effort: a storage failure only
// disables persistence, never the program itself).
func (t *RunCodeTool) newRunCodeRun(ctx context.Context) *runCodeRun {
	run := &runCodeRun{
		tool:  t,
		ctx:   ctx,
		runID: randomRunID(),
		start: time.Now(),
	}
	if t.DispatchDir == "" {
		return run
	}
	dir := filepath.Join(t.DispatchDir, run.runID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return run
	}
	run.spill = common.NewSpillStore(filepath.Join(dir, "spill"))
	log, err := common.NewDispatchLog(filepath.Join(dir, "dispatch.jsonl"))
	if err != nil {
		return run
	}
	run.log = log
	return run
}

// closeLog flushes and closes the durable dispatch log for this run.
func (r *runCodeRun) closeLog() {
	if r.log != nil {
		_ = r.log.Close()
	}
}

// randomRunID returns a 128-bit random hex identifier for one run_code run.
func randomRunID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// subCallNames returns the registered tool names exposed as `tools.<name>`
// bindings, excluding run_code itself so a program cannot recurse into the
// outer transport.
func (t *RunCodeTool) subCallNames() []string {
	if t.Registry == nil {
		return nil
	}
	var names []string
	for _, tool := range t.Registry.All() {
		name := tool.Schema().Name
		if name == RunCodeToolName {
			continue
		}
		names = append(names, name)
	}
	return names
}

// installToolsBinding builds the `tools` module: one attribute per registered
// tool (except run_code), each dispatching through the guarded agent pipeline.
func (r *runCodeRun) installToolsBinding(pyCtx py.Context) error {
	tool := r.tool
	impl := &py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "tools",
			Doc:  "tools — registered tool bindings; calls dispatch through the guarded permission/jail pipeline.",
		},
	}
	for _, name := range tool.subCallNames() {
		nm := name
		impl.Methods = append(impl.Methods, py.MustNewMethod(nm, func(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
			return r.dispatch(nm, args, kwargs)
		}, 0, "Dispatch a call to the "+nm+" tool (guarded pipeline)."))
	}
	mod, err := pyCtx.Store().NewModule(pyCtx, impl)
	if err != nil {
		return err
	}
	// The module is registered in the store ("import tools" works too); the
	// __main__ globals get the bare `tools` name below so `tools.name(...)`
	// resolves without an import.
	r.toolsMod = mod
	return nil
}

// dispatch is the Python-facing sub-call binding: converts the argument object
// to JSON, dispatches through the agent's guarded pipeline, records the
// dispatch in the durable log (spill-capping oversized results), and returns
// the tool result to the program. Sub-call failures raise a Python exception
// carrying the guarded pipeline's error text.
func (r *runCodeRun) dispatch(name string, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	input, err := bindingArgsToJSON(name, args, kwargs)
	if err != nil {
		return nil, err
	}

	dispatcher, ok := agentic.ToolDispatcherFrom(r.ctx)
	if !ok {
		return nil, py.ExceptionNewf(py.RuntimeError,
			"tool %s: run_code has no agent dispatch context; sub-calls are unavailable", name)
	}

	r.seq++
	seq := r.seq
	callID := fmt.Sprintf("%s:sub:%d", r.runID, seq)
	started := time.Now()
	result, runErr := dispatcher(r.ctx, name, input, callID)

	entry := common.DispatchEntry{
		RunID:      r.runID,
		Seq:        seq,
		CallID:     callID,
		Tool:       name,
		Arguments:  input,
		StartedAt:  started,
		FinishedAt: time.Now(),
	}
	if runErr != nil {
		entry.OK = false
		entry.Error = runErr.Error()
	} else {
		entry.OK = true
		entry.Result, entry.SpillPath = r.capResult(name, result.Output)
	}
	entry.DurationMS = time.Since(started).Milliseconds()
	if r.log != nil {
		// The dispatch log is durable diagnostics: a write failure must never
		// fail the sub-call itself.
		_ = r.log.Append(entry)
	}

	if runErr != nil {
		return nil, py.ExceptionNewf(py.RuntimeError, "tool %s failed: %v", name, runErr)
	}
	return py.String(result.Output), nil
}

// capResult bounds the inline result stored in the dispatch log: results over
// the cap spill verbatim to the run's spill store and the entry records a
// head/tail preview plus the spill path. A spill failure keeps the result
// inline (never hides it).
func (r *runCodeRun) capResult(name, output string) (preview, spillPath string) {
	capBytes := r.tool.maxLogResultBytes()
	if len(output) <= capBytes || r.spill == nil {
		return output, ""
	}
	path, err := r.spill.Save(name+".txt", output)
	if err != nil {
		return output, ""
	}
	half := capBytes / 2
	head := cutStringHead(output, half)
	tail := truncateStringTail(output, capBytes-half)
	return head + tail, path
}

// bindingArgsToJSON converts a binding call's argument object to a JSON input
// string. One positional argument (any JSON-serializable value) or keyword
// arguments are accepted; the argument is passed through losslessly so the
// dispatched tool receives exactly what the program supplied.
func bindingArgsToJSON(name string, args py.Tuple, kwargs py.StringDict) (string, error) {
	var goVal any
	var err error
	switch {
	case len(args) >= 1:
		if len(kwargs) > 0 {
			return "", py.ExceptionNewf(py.TypeError, "%s() accepts either one positional arguments object or keyword arguments, not both", name)
		}
		goVal, err = compat.PyToGo(args[0])
	case len(kwargs) > 0:
		goVal, err = compat.PyToGo(kwargs)
	default:
		return "", py.ExceptionNewf(py.TypeError, "%s() requires an arguments object, e.g. tools.%s({...})", name, name)
	}
	if err != nil {
		return "", err
	}
	data, merr := json.Marshal(goVal)
	if merr != nil {
		return "", py.ExceptionNewf(py.TypeError, "%s() arguments must be JSON-serializable: %v", name, merr)
	}
	return string(data), nil
}

// runProgram creates a fresh gpython context, installs the jail-aware os
// module and the tools binding, and runs the program. The interpreter runs in
// a dedicated goroutine so a timeout or context cancellation can return
// promptly even though gpython does not support cooperative cancellation.
func (t *RunCodeTool) runProgram(ctx context.Context, p runCodeInput) (string, error) {
	// Capture stdout/stderr through an os.Pipe (gpython's py.File wraps
	// *os.File), mirroring PythonTool.
	r, w, err := os.Pipe()
	if err != nil {
		return "", toolErr(RunCodeToolName, "io_error", fmt.Sprintf("Cannot create output pipe: %v", err))
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

	// Install the same jail-confined, curated `os` module used by PythonTool:
	// the program's own file operations can never escape the project jail.
	if err := installGoaFsModules(pyCtx, newPyFileScope(t.ProjectDir, t.Jail)); err != nil {
		pyCtx.Close()
		_ = w.Close()
		<-readDone
		return "", toolErr(RunCodeToolName, "setup_error", fmt.Sprintf("Cannot install os file module: %v", err))
	}

	run := t.newRunCodeRun(ctx)
	if err := run.installToolsBinding(pyCtx); err != nil {
		pyCtx.Close()
		_ = w.Close()
		<-readDone
		return "", toolErr(RunCodeToolName, "setup_error", fmt.Sprintf("Cannot install tools binding: %v", err))
	}

	comp, err := compilePythonCode(p.Code)
	if err != nil {
		pyCtx.Close()
		_ = w.Close()
		<-readDone
		return "", toolErr(RunCodeToolName, "execution_error", formatPythonError(err))
	}

	// Run the program in a __main__ module whose globals expose the bare
	// `tools` binding. NewModule first (so globals can be seeded) then
	// RunCode with that module — the same execution path PythonTool takes,
	// with the binding injected.
	mainMod, err := pyCtx.Store().NewModule(pyCtx, &py.ModuleImpl{
		Info: py.ModuleInfo{Name: "__main__", FileDesc: "<run_code>"},
	})
	if err != nil {
		pyCtx.Close()
		_ = w.Close()
		<-readDone
		return "", toolErr(RunCodeToolName, "setup_error", fmt.Sprintf("Cannot create main module: %v", err))
	}
	mainMod.Globals["tools"] = run.toolsMod

	runDone := make(chan error, 1)
	go func() {
		_, runErr := py.RunCode(pyCtx, comp, "<run_code>", mainMod)
		run.closeLog()
		pyCtx.Close()
		_ = w.Close()
		runDone <- runErr
	}()

	timeout := normalizePythonTimeout(p.Timeout, t.TimeoutSeconds)
	select {
	case err := <-runDone:
		<-readDone
		if err != nil {
			return out.String(), toolErr(RunCodeToolName, "execution_error", formatPythonError(err))
		}
		return out.String(), nil
	case <-ctx.Done():
		run.closeLog()
		return "", toolErr(RunCodeToolName, "cancelled", fmt.Sprintf("Execution cancelled: %v", ctx.Err()))
	case <-time.After(time.Duration(timeout) * time.Second):
		run.closeLog()
		return "", toolErr(RunCodeToolName, "timeout", fmt.Sprintf("Execution timed out after %ds", timeout))
	}
}

// truncateOutput applies configured line/byte limits and sanitizes the result
// for the TUI, mirroring PythonTool.
func (t *RunCodeTool) truncateOutput(output string) string {
	maxLines := t.MaxOutputLines
	if maxLines <= 0 {
		maxLines = DefaultMaxLines
	}
	maxBytes := t.MaxOutputBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	res := TruncateTail(output, maxLines, maxBytes)
	content := ansi.Sanitize(res.Content)
	if res.Truncated {
		path, saveErr := SaveTruncatedOutput(output)
		if saveErr == nil {
			return fmt.Sprintf("Output truncated: %s\nFull output saved to: %s\n%s", TruncResString(res), path, content)
		}
		return fmt.Sprintf("Output truncated: %s\n%s", TruncResString(res), content)
	}
	return content
}

// Access returns the tool access category for run_code executions.
func (t *RunCodeTool) Access(input string) ToolAccess {
	return ToolAccess{Category: "shell"}
}

// MutatesState reports that a successful run_code execution may change shared
// state (the program and its sub-calls can write files). The loop guardrails
// treat it as a state mutation that resets the no-progress repeat horizon.
func (t *RunCodeTool) MutatesState() bool { return true }

//go:embed run_code.short.md run_code.long.md
var runCodeDocs embed.FS

// ShortDoc returns the short documentation for run_code.
func (t *RunCodeTool) ShortDoc() string { return readDoc(runCodeDocs, "run_code.short.md") }

// LongDoc returns the long documentation for run_code.
func (t *RunCodeTool) LongDoc() string { return readDoc(runCodeDocs, "run_code.long.md") }

// Examples returns example run_code tool invocations.
func (t *RunCodeTool) Examples() []string {
	return []string{
		`{"code": "print(tools.search({\"query\": \"TODO\"}))", "description": "Search TODO markers"}`,
		`{"code": "a = tools.read({\"path\": \"go.mod\"})\nprint(a)", "description": "Read go.mod"}`,
	}
}

// LoopHints supplies tool-loop-controller metadata for the run_code tool.
func (t *RunCodeTool) LoopHints() agentic.ToolLoopHints {
	return agentic.ToolLoopHints{HealArg: "code", Status: runCodeRunStatus}
}

// Ensure RunCodeTool implements the required interfaces.
var (
	_ agentic.Tool          = (*RunCodeTool)(nil)
	_ agentic.ContextTool   = (*RunCodeTool)(nil)
	_ agentic.LoopAnnotated = (*RunCodeTool)(nil)
	_ Documentable          = (*RunCodeTool)(nil)
	_ Accessor              = (*RunCodeTool)(nil)
)

// runCodeRunStatus returns a short status line for the TUI while the run_code
// tool is in flight.
func runCodeRunStatus(arguments string) string {
	var p runCodeInput
	if err := json.Unmarshal([]byte(arguments), &p); err != nil || p.Description == "" {
		return "Running run_code..."
	}
	line := strings.Split(p.Description, "\n")[0]
	if len(line) > 60 {
		line = string([]rune(line)[:57]) + "..."
	}
	return "Running: " + line
}
