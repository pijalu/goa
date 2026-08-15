// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tools/common"
)

// fakeTool is a minimal agentic.Tool used to populate a run_code sub-call
// surface without touching real tool implementations.
type fakeTool struct {
	name string
	fn   func(input string) (string, error)
}

func (f *fakeTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{Name: f.name, Description: "fake " + f.name, Schema: map[string]any{"type": "object"}}
}

func (f *fakeTool) Execute(input string) (string, error) {
	if f.fn != nil {
		return f.fn(input)
	}
	return "ok:" + f.name, nil
}

func (f *fakeTool) IsRetryable(err error) bool { return false }

// recordedCall captures one sub-call seen by the recorder dispatcher.
type recordedCall struct {
	Name   string
	Input  string
	CallID string
}

// dispatchRecorder is a dispatcher that records every sub-call and, when it
// has a registry, delegates to the registered tool exactly like the agent's
// guarded pipeline does (registry lookup + execution). A simulated denial or
// error text short-circuits before delegation.
type dispatchRecorder struct {
	mu      sync.Mutex
	calls   []recordedCall
	result  string
	errText string
	// reg, when set, is consulted for the sub-call's tool execution.
	reg *ToolRegistry
	// denySimulated marks whether the dispatcher simulates a pipeline denial.
	denySimulated bool
}

func newDispatchRecorder() *dispatchRecorder {
	return &dispatchRecorder{result: "R:"}
}

func (r *dispatchRecorder) dispatch(ctx context.Context, name, input, callID string) (agentic.ToolResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, recordedCall{Name: name, Input: input, CallID: callID})
	simulated := r.denySimulated
	result, errText := r.result, r.errText
	reg := r.reg
	r.mu.Unlock()
	if simulated {
		return agentic.ToolResult{}, fmt.Errorf("denied by guard: %s", errText)
	}
	if errText != "" {
		return agentic.ToolResult{}, fmt.Errorf("%s", errText)
	}
	if reg != nil {
		if tool, ok := reg.Get(name); ok {
			out, err := tool.Execute(input)
			return agentic.ToolResult{Output: out, Error: err}, err
		}
		return agentic.ToolResult{}, fmt.Errorf("unknown tool: %s", name)
	}
	return agentic.ToolResult{Output: result + name}, nil
}

func (r *dispatchRecorder) snapshot() []recordedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// newRunCodeFixture builds a RunCodeTool wired to a registry of fake tools and
// a recorder dispatcher, with the dispatch log + spill store in t.TempDir().
func newRunCodeFixture(t *testing.T, mods ...func(*RunCodeTool)) (*RunCodeTool, *dispatchRecorder) {
	t.Helper()
	reg := NewToolRegistry()
	reg.Register(&fakeTool{name: "read", fn: func(input string) (string, error) {
		var p struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal([]byte(input), &p)
		return "content:" + p.Path, nil
	}})
	reg.Register(&fakeTool{name: "search", fn: func(input string) (string, error) {
		var p struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal([]byte(input), &p)
		return "hits:" + p.Query, nil
	}})
	reg.Register(&fakeTool{name: "bash", fn: func(input string) (string, error) {
		var p struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal([]byte(input), &p)
		return "cmd-out:" + p.Command, nil
	}})
	reg.Register(&fakeTool{name: "write", fn: func(input string) (string, error) {
		return "written", nil
	}})
	// A fake that returns a large result, for spill tests.
	reg.Register(&fakeTool{name: "big", fn: func(input string) (string, error) {
		return strings.Repeat("x", 5000), nil
	}})

	rec := newDispatchRecorder()
	rec.reg = reg
	tool := &RunCodeTool{
		ProjectDir:       t.TempDir(),
		Jail:             true,
		Registry:         reg,
		DispatchDir:      t.TempDir(),
		MaxLogResultBytes: 256,
	}
	for _, m := range mods {
		m(tool)
	}
	return tool, rec
}

// runWithDispatcher executes a run_code program with the recorder acting as
// the agent dispatch context.
func runWithDispatcher(t *testing.T, tool *RunCodeTool, rec *dispatchRecorder, code string) (string, error) {
	t.Helper()
	ctx := agentic.WithToolDispatcher(context.Background(), rec.dispatch)
	input, _ := json.Marshal(map[string]any{"code": code, "description": "test program"})
	return tool.ExecuteContext(ctx, string(input))
}

// readDispatchEntries reads every JSON line of the single dispatch log written
// under dir (the run subdirectory), decoding each into a DispatchEntry.
func readDispatchEntries(t *testing.T, dir string) []common.DispatchEntry {
	t.Helper()
	var entries []common.DispatchEntry
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var e common.DispatchEntry
			if derr := json.Unmarshal([]byte(line), &e); derr != nil {
				return derr
			}
			entries = append(entries, e)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading dispatch log: %v", err)
	}
	return entries
}

// TestRunCodeTool_Schema verifies the run_code schema shape.
func TestRunCodeTool_Schema(t *testing.T) {
	tool := &RunCodeTool{}
	s := tool.Schema()
	if s.Name != RunCodeToolName {
		t.Errorf("Name = %q, want %q", s.Name, RunCodeToolName)
	}
	props, ok := s.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("missing properties")
	}
	for _, key := range []string{"code", "description", "timeout"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing %s property", key)
		}
	}
	req, ok := s.Schema["required"].([]string)
	if !ok || len(req) != 2 || req[0] != "code" || req[1] != "description" {
		t.Errorf("required = %v, want [code description]", req)
	}
}

// TestRunCodeTool_FanOutToOneSubCall verifies a single run_code call can
// dispatch multiple tools.
func TestRunCodeTool_FanOutToOneSubCall(t *testing.T) {
	tool, rec := newRunCodeFixture(t)
	code := `
tools.read({"path": "a.txt"})
tools.search({"query": "foo"})
tools.bash({"command": "echo hi"})
tools.write({"path": "b.txt", "content": "x"})
print("DONE")
`
	out, err := runWithDispatcher(t, tool, rec, code)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(out, "DONE") {
		t.Errorf("output = %q, want DONE", out)
	}
	calls := rec.snapshot()
	if len(calls) < 3 {
		t.Fatalf("dispatched %d sub-calls, want >= 3", len(calls))
	}
	wantNames := []string{"read", "search", "bash", "write"}
	if len(calls) != len(wantNames) {
		t.Fatalf("dispatched %d sub-calls, want %d: %+v", len(calls), len(wantNames), calls)
	}
	for i, w := range wantNames {
		if calls[i].Name != w {
			t.Errorf("sub-call %d name = %q, want %q", i, calls[i].Name, w)
		}
	}
}

// TestRunCodeTool_SubCallResultsReachProgram verifies tool results are
// returned to the program as strings.
func TestRunCodeTool_SubCallResultsReachProgram(t *testing.T) {
	tool, rec := newRunCodeFixture(t)
	code := `
r1 = tools.read({"path": "a.txt"})
r2 = tools.search({"query": "zzz"})
print("READ:", r1)
print("SEARCH:", r2)
`
	out, err := runWithDispatcher(t, tool, rec, code)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(out, "READ: content:a.txt") {
		t.Errorf("output = %q, want READ: content:a.txt", out)
	}
	if !strings.Contains(out, "SEARCH: hits:zzz") {
		t.Errorf("output = %q, want SEARCH: hits:zzz", out)
	}
}

// TestRunCodeTool_DispatchLogRecordsSubCalls verifies every sub-call is
// durably logged with tool name, arguments, timing, and outcome.
func TestRunCodeTool_DispatchLogRecordsSubCalls(t *testing.T) {
	tool, rec := newRunCodeFixture(t)
	code := `
tools.read({"path": "a.txt"})
tools.search({"query": "foo"})
tools.write({"path": "b.txt", "content": "x"})
print("DONE")
`
	if _, err := runWithDispatcher(t, tool, rec, code); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	entries := readDispatchEntries(t, tool.DispatchDir)
	if len(entries) != 3 {
		t.Fatalf("logged %d entries, want 3: %+v", len(entries), entries)
	}
	names := []string{"read", "search", "write"}
	for i, e := range entries {
		if e.Tool != names[i] {
			t.Errorf("entry %d tool = %q, want %q", i, e.Tool, names[i])
		}
		if !e.OK {
			t.Errorf("entry %d OK = false, want true (error=%q)", i, e.Error)
		}
		if e.RunID == "" || e.CallID == "" {
			t.Errorf("entry %d missing RunID/CallID: %+v", i, e)
		}
		if e.Seq != i+1 {
			t.Errorf("entry %d Seq = %d, want %d", i, e.Seq, i+1)
		}
		if e.Arguments == "" {
			t.Errorf("entry %d missing arguments", i)
		}
		if e.StartedAt.IsZero() || e.FinishedAt.IsZero() {
			t.Errorf("entry %d missing timing", i)
		}
		if e.Result == "" {
			t.Errorf("entry %d missing result", i)
		}
	}
	// The log survives: the JSONL file must exist after the run.
	if len(entries) != 3 {
		return
	}
}

// TestRunCodeTool_DispatchLogSpillCapsLargeResults verifies oversized sub-call
// results are spill-capped in the log (preview bounded + spill file on disk).
func TestRunCodeTool_DispatchLogSpillCapsLargeResults(t *testing.T) {
	tool, rec := newRunCodeFixture(t)
	code := `
tools.big({})
print("DONE")
`
	if _, err := runWithDispatcher(t, tool, rec, code); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	entries := readDispatchEntries(t, tool.DispatchDir)
	if len(entries) != 1 {
		t.Fatalf("logged %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Tool != "big" {
		t.Fatalf("tool = %q, want big", e.Tool)
	}
	if e.SpillPath == "" {
		t.Fatal("expected a spill path for the oversized result")
	}
	if len(e.Result) > tool.maxLogResultBytes() {
		t.Errorf("logged result %d bytes, want <= %d", len(e.Result), tool.maxLogResultBytes())
	}
	if _, err := os.Stat(e.SpillPath); err != nil {
		t.Errorf("spill file missing: %v", err)
	}
}

// TestRunCodeTool_NoSpillKeepsFullResult verifies a result under the cap is
// stored inline without a spill path.
func TestRunCodeTool_NoSpillKeepsFullResult(t *testing.T) {
	tool, rec := newRunCodeFixture(t)
	tool.MaxLogResultBytes = 1 << 20 // huge cap: nothing spills
	code := `
tools.read({"path": "small.txt"})
print("DONE")
`
	if _, err := runWithDispatcher(t, tool, rec, code); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	entries := readDispatchEntries(t, tool.DispatchDir)
	if len(entries) != 1 {
		t.Fatalf("logged %d entries, want 1", len(entries))
	}
	if entries[0].SpillPath != "" {
		t.Errorf("unexpected spill path %q for a small result", entries[0].SpillPath)
	}
	if !strings.Contains(entries[0].Result, "content:small.txt") {
		t.Errorf("result = %q, want full content inline", entries[0].Result)
	}
}

// TestRunCodeTool_SubCallDeniedRaises verifies a sub-call rejected by the
// guarded pipeline surfaces to the program as an exception the program can
// catch (the same denial a direct call would see).
func TestRunCodeTool_SubCallDeniedRaises(t *testing.T) {
	tool, rec := newRunCodeFixture(t)
	rec.denySimulated = true
	rec.errText = "planner restriction"
	code := `
try:
    tools.bash({"command": "rm -rf /"})
    print("NOT-DENIED")
except Exception as e:
    print("DENIED:", e)
`
	out, err := runWithDispatcher(t, tool, rec, code)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(out, "DENIED: tool bash failed: denied by guard: planner restriction") {
		t.Errorf("output = %q, want the guard denial surfaced to the program", out)
	}
}

// TestRunCodeTool_NoDispatchContextFailsCleanly verifies a run_code execution
// without an agent dispatch context cannot dispatch sub-calls (safe default).
func TestRunCodeTool_NoDispatchContextFailsCleanly(t *testing.T) {
	tool, _ := newRunCodeFixture(t)
	code := `tools.read({"path": "a.txt"})`
	input, _ := json.Marshal(map[string]any{"code": code, "description": "no ctx"})
	// No WithToolDispatcher: the program must fail with a clean error.
	_, err := tool.ExecuteContext(context.Background(), string(input))
	if err == nil {
		t.Fatal("expected an error when no dispatch context is present")
	}
	if !strings.Contains(err.Error(), "no agent dispatch context") {
		t.Errorf("error = %v, want no-agent-dispatch-context message", err)
	}
}

// TestRunCodeTool_RunCodeNotInBinding verifies run_code itself is not exposed
// as a binding attribute (no recursion into the outer transport).
func TestRunCodeTool_RunCodeNotInBinding(t *testing.T) {
	tool, rec := newRunCodeFixture(t)
	code := `
try:
    tools.run_code({"code": "print(1)", "description": "nested"})
    print("NESTED-CALLED")
except Exception as e:
    print("NESTED-BLOCKED:", e)
`
	out, err := runWithDispatcher(t, tool, rec, code)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(out, "NESTED-BLOCKED:") || strings.Contains(out, "NESTED-BLOCKED: ATTRIBUTE-NOT-RAISED") {
		t.Errorf("output = %q, want run_code absent from the binding", out)
	}
	if !strings.Contains(out, "has no attribute") {
		t.Errorf("output = %q, want an AttributeError for the absent run_code binding", out)
	}
	if calls := rec.snapshot(); len(calls) != 0 {
		t.Errorf("run_code sub-call dispatched %d calls, want 0", len(calls))
	}
}

// TestRunCodeTool_JailConfinesFileAccess verifies the program's own os file
// operations cannot escape the project jail.
func TestRunCodeTool_JailConfinesFileAccess(t *testing.T) {
	tool, _ := newRunCodeFixture(t)
	code := `
import os
try:
    print(os.listdir("/etc"))
    print("ESCAPED")
except Exception as e:
    print("JAILED:", e)
`
	out, err := runWithDispatcher(t, tool, newDispatchRecorder(), code)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if strings.Contains(out, "ESCAPED") {
		t.Fatalf("os.listdir(/etc) escaped the jail: %q", out)
	}
	if !strings.Contains(out, "JAILED:") || strings.Contains(out, "JAILED: NOT-RAISED") {
		t.Errorf("output = %q, want the jail to raise for /etc access", out)
	}
	if !strings.Contains(out, "escapes jail root") {
		t.Errorf("output = %q, want the jail escape message", out)
	}
}

// TestRunCodeTool_OsSystemAbsent verifies the unsafe os.system primitive is
// not present in the jailed os module.
func TestRunCodeTool_OsSystemAbsent(t *testing.T) {
	tool, _ := newRunCodeFixture(t)
	code := `
import os
try:
    os.system("echo pwned")
    print("SYSTEM-CALLED")
except Exception as e:
    print("NO-SYSTEM:", e)
`
	out, err := runWithDispatcher(t, tool, newDispatchRecorder(), code)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if strings.Contains(out, "SYSTEM-CALLED") {
		t.Fatalf("os.system was available in the jail: %q", out)
	}
	if !strings.Contains(out, "NO-SYSTEM:") || strings.Contains(out, "NO-SYSTEM: NOT-RAISED") {
		t.Errorf("output = %q, want os.system absent", out)
	}
	if !strings.Contains(out, "has no attribute 'system'") {
		t.Errorf("output = %q, want AttributeError for os.system", out)
	}
}

// TestRunCodeTool_SubprocessAbsent verifies subprocess is not importable.
func TestRunCodeTool_SubprocessAbsent(t *testing.T) {
	tool, _ := newRunCodeFixture(t)
	code := `
try:
    import subprocess
    print("SUBPROCESS-OK")
except Exception as e:
    print("NO-SUBPROCESS:", e)
`
	out, err := runWithDispatcher(t, tool, newDispatchRecorder(), code)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if strings.Contains(out, "SUBPROCESS-OK") {
		t.Fatalf("subprocess import succeeded in the jail: %q", out)
	}
	if !strings.Contains(out, "NO-SUBPROCESS:") || strings.Contains(out, "NO-SUBPROCESS: NOT-RAISED") {
		t.Errorf("output = %q, want subprocess import blocked", out)
	}
	if !strings.Contains(out, "Failed to resolve") {
		t.Errorf("output = %q, want the unsupported-module message", out)
	}
}

// TestRunCodeTool_UnknownBindingAbsent verifies calling an unregistered tool
// attribute raises AttributeError without reaching the dispatcher.
func TestRunCodeTool_UnknownBindingAbsent(t *testing.T) {
	tool, rec := newRunCodeFixture(t)
	code := `
try:
    tools.nonexistent({"x": 1})
    print("UNKNOWN-CALLED")
except Exception as e:
    print("UNKNOWN-BLOCKED:", e)
`
	out, err := runWithDispatcher(t, tool, rec, code)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(out, "UNKNOWN-BLOCKED:") || strings.Contains(out, "UNKNOWN-BLOCKED: NOT-RAISED") {
		t.Errorf("output = %q, want an error for the unknown binding", out)
	}
	if !strings.Contains(out, "has no attribute") {
		t.Errorf("output = %q, want AttributeError for the unknown binding", out)
	}
	if calls := rec.snapshot(); len(calls) != 0 {
		t.Errorf("unknown binding dispatched %d calls, want 0", len(calls))
	}
}

// TestRunCodeTool_MaxProgramBytes verifies oversized programs are rejected
// before any execution.
func TestRunCodeTool_MaxProgramBytes(t *testing.T) {
	tool, _ := newRunCodeFixture(t)
	tool.MaxProgramBytes = 10
	code := `print("a very long program that exceeds the configured cap")`
	input, _ := json.Marshal(map[string]any{"code": code, "description": "too big"})
	_, err := tool.ExecuteContext(context.Background(), string(input))
	if err == nil || !strings.Contains(err.Error(), "maximum is 10 bytes") {
		t.Errorf("error = %v, want program_too_large", err)
	}
}

// TestRunCodeTool_RequiredArgs verifies code/description validation.
func TestRunCodeTool_RequiredArgs(t *testing.T) {
	tool := &RunCodeTool{}
	if _, err := tool.Execute(`{"description": "no code"}`); err == nil {
		t.Error("expected missing_code error")
	}
	if _, err := tool.Execute(`{"code": "print(1)", "description": "  "}`); err == nil {
		t.Error("expected missing_description error")
	}
	if _, err := tool.Execute(`not json`); err == nil {
		t.Error("expected invalid_input error")
	}
}

// TestRunCodeTool_Timeout verifies a program that exceeds the timeout is cut
// off with a timeout error.
func TestRunCodeTool_Timeout(t *testing.T) {
	tool, _ := newRunCodeFixture(t)
	code := `
import time
time.sleep(5)
print("never")
`
	input, _ := json.Marshal(map[string]any{"code": code, "description": "sleep", "timeout": 1})
	start := time.Now()
	_, err := tool.ExecuteContext(context.Background(), string(input))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Errorf("timeout took %v, want prompt return", elapsed)
	}
}

// TestRunCodeTool_KeywordArgs verifies keyword arguments are accepted as the
// sub-call input object.
func TestRunCodeTool_KeywordArgs(t *testing.T) {
	tool, rec := newRunCodeFixture(t)
	code := `
tools.read(path="kw.txt")
print("DONE")
`
	out, err := runWithDispatcher(t, tool, rec, code)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(out, "DONE") {
		t.Errorf("output = %q, want DONE", out)
	}
	calls := rec.snapshot()
	if len(calls) != 1 || calls[0].Name != "read" {
		t.Fatalf("calls = %+v, want one read call", calls)
	}
	var arg struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(calls[0].Input), &arg); err != nil || arg.Path != "kw.txt" {
		t.Errorf("input = %q, want {\"path\":\"kw.txt\"}", calls[0].Input)
	}
}
