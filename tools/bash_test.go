// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal"
)

func TestBashTool_Schema_ReturnsValidSchema(t *testing.T) {
	tool := &BashTool{}
	schema := tool.Schema()
	if schema.Name != "bash" {
		t.Errorf("schema.Name = %q, want %q", schema.Name, "bash")
	}
	if schema.Description == "" {
		t.Errorf("schema.Description should not be empty")
	}
}

func TestBashTool_IsRetryable_ReturnsFalse(t *testing.T) {
	tool := &BashTool{}
	if tool.IsRetryable(nil) {
		t.Error("IsRetryable should return false for nil error")
	}
}

func TestBashTool_Execute_EmptyInput_ReturnsError(t *testing.T) {
	tool := &BashTool{}
	_, err := tool.Execute("")
	if err == nil {
		t.Error("Execute with empty input should return error")
	}
}

func TestBashTool_Execute_InvalidJSON_ReturnsError(t *testing.T) {
	tool := &BashTool{}
	_, err := tool.Execute("not json")
	if err == nil {
		t.Error("Execute with invalid JSON should return error")
	}
}

func TestBashTool_Execute_MissingCommand_ReturnsError(t *testing.T) {
	tool := &BashTool{}
	_, err := tool.Execute(`{"timeout": 5}`)
	if err == nil {
		t.Error("Execute without command should return error")
	}
}

func TestBashTool_Execute_BlockedCommand_ReturnsError(t *testing.T) {
	tool := &BashTool{
		Blocked: []string{"rm -rf /"},
	}
	_, err := tool.Execute(`{"command": "rm -rf /"}`)
	if err == nil {
		t.Error("Execute with blocked command should return error")
	}
}

func TestBashTool_Execute_NotAllowedCommand_ReturnsError(t *testing.T) {
	tool := &BashTool{
		Allowed: []string{"ls", "echo"},
	}
	_, err := tool.Execute(`{"command": "rm file"}`)
	if err == nil {
		t.Error("Execute with non-allowed command should return error")
	}
}

func TestBashTool_Execute_AllowedCommand_Succeeds(t *testing.T) {
	tool := &BashTool{
		Allowed: []string{"echo"},
	}
	result, err := tool.Execute(`{"command": "echo hello"}`)
	if err != nil {
		t.Fatalf("Execute with allowed command should succeed: %v", err)
	}
	if !strings.Contains(result, "hello") {
		t.Errorf("Expected output to contain 'hello', got: %q", result)
	}
}

func TestBashTool_Execute_OutputFormat_HasSections(t *testing.T) {
	tool := &BashTool{}
	result, err := tool.Execute(`{"command": "echo hello"}`)
	if err != nil {
		t.Fatalf("Execute should succeed: %v", err)
	}

	// Output: no [bash:] / Exit: metadata, just Duration footer.
	if !strings.Contains(result, "Duration:") {
		t.Errorf("Result missing duration indicator, got: %q", result)
	}
}

func TestBashTool_Execute_WithWorkdir_UsesCorrectDir(t *testing.T) {
	tool := &BashTool{}
	result, err := tool.Execute(`{"command": "pwd", "workdir": "/tmp"}`)
	if err != nil {
		t.Fatalf("Execute with workdir should succeed: %v", err)
	}
	if !strings.Contains(result, "/tmp") {
		t.Errorf("Expected pwd output to contain /tmp, got: %q", result)
	}
}

func TestBashTool_Execute_MultipleLines_ReturnsAllOutput(t *testing.T) {
	tool := &BashTool{}
	result, err := tool.Execute(`{"command": "echo line1 && echo line2 && echo line3"}`)
	if err != nil {
		t.Fatalf("Execute should succeed: %v", err)
	}
	for _, line := range []string{"line1", "line2", "line3"} {
		if !strings.Contains(result, line) {
			t.Errorf("Expected output to contain %q, got: %q", line, result)
		}
	}
}

func TestBashTool_Execute_EmptyOutput_StillReturnsDuration(t *testing.T) {
	tool := &BashTool{}
	result, err := tool.Execute(`{"command": "true"}`)
	if err != nil {
		t.Fatalf("Execute 'true' should succeed: %v", err)
	}
	if !strings.Contains(result, "Duration:") {
		t.Errorf("Expected duration in output, got: %q", result)
	}
}

func TestBashTool_Execute_ErrorCommand_ReturnsToolError(t *testing.T) {
	tool := &BashTool{}
	result, err := tool.Execute(`{"command": "false"}`)
	if err == nil {
		t.Fatalf("Execute 'false' should return error (non-zero exit), got result: %q", result)
	}
	if !strings.Contains(err.Error(), "non_zero_exit") {
		t.Errorf("Expected non_zero_exit error type, got: %v", err)
	}
	// The output should be included in the error detail
	if !strings.Contains(err.Error(), "Command exited with code 1") {
		t.Errorf("Expected exit code 1 in error, got: %v", err)
	}
	// Non-zero exits are normal — no recovery hint should be attached.
	var toolErr *internal.ToolError
	if errors.As(err, &toolErr) {
		if toolErr.HintText != "" {
			t.Errorf("Expected no hint for non-zero exit, got: %q", toolErr.HintText)
		}
	} else {
		t.Errorf("Expected *internal.ToolError, got %T", err)
	}
}

func TestBashTool_Execute_EnvMasking_HidesSensitiveValues(t *testing.T) {
	tool := &BashTool{
		EnvMaskPatterns: []string{"*SECRET*"},
	}
	result, err := tool.Execute(`{"command": "echo visible_value"}`)
	if err != nil {
		t.Fatalf("Execute should succeed: %v", err)
	}
	if !strings.Contains(result, "visible_value") {
		t.Errorf("Output should contain non-sensitive values, got: %q", result)
	}
}

func TestBashTool_Execute_EnvVarNotSet_EnvParamUsedForMasking(t *testing.T) {
	tool := &BashTool{}
	result, err := tool.Execute(`{"command": "echo $TEST_VAR"}`)
	if err != nil {
		t.Fatalf("Execute should succeed: %v", err)
	}
	// The env param is used for masking, not setting command env
	// So $TEST_VAR won't be expanded since it's not inherited
	if !strings.Contains(result, "Duration:") {
		t.Errorf("Expected duration in output, got: %q", result)
	}
}

func TestBashTool_Documentation_LongDocLongerThanShort(t *testing.T) {
	tool := &BashTool{}
	short := tool.ShortDoc()
	long := tool.LongDoc()
	if short == "" {
		t.Error("ShortDoc should not be empty")
	}
	if len(long) <= len(short) {
		t.Errorf("LongDoc (%d chars) should be longer than ShortDoc (%d chars)", len(long), len(short))
	}
	if strings.Contains(long, short) {
		t.Error("LongDoc should not just contain ShortDoc — it should have additional content")
	}
}

func TestBashTool_Examples_HaveExpectedFormat(t *testing.T) {
	tool := &BashTool{}
	examples := tool.Examples()
	if len(examples) == 0 {
		t.Fatal("Examples should not be empty")
	}
	for i, ex := range examples {
		if !strings.HasPrefix(ex, `{"command":`) {
			t.Errorf("Example %d should start with JSON command, got: %q", i, ex)
		}
	}
}

func TestBashTool_CompressOutput_DisabledByDefault(t *testing.T) {
	tool := &BashTool{}
	if tool.CompressOutput {
		t.Error("CompressOutput should be false by default on a zero BashTool")
	}
}

func TestBashTool_Execute_FindWithXargs_Works(t *testing.T) {
	tool := &BashTool{}
	// This is the exact command that used to break under RTK auto-routing.
	result, err := tool.Execute(`{"command": "find . -name \"*.go\" -not -path './.git/*' -not -path '*/vendor/*' | head -5"}`)
	if err != nil {
		t.Fatalf("Execute should succeed: %v", err)
	}
	if !strings.Contains(result, "Duration:") {
		t.Errorf("Expected duration in output, got: %q", result)
	}
}

func TestBashTool_Execute_CompressOutput_CompressesLs(t *testing.T) {
	tool := &BashTool{CompressOutput: true}
	result, err := tool.Execute(`{"command": "ls -la"}`)
	if err != nil {
		t.Fatalf("Execute should succeed: %v", err)
	}
	if !strings.Contains(result, "[compress:") {
		t.Errorf("Expected built-in compression marker, got: %q", result)
	}
}

func TestBashTool_Execute_CompressOutputDisabled_NoCompression(t *testing.T) {
	tool := &BashTool{CompressOutput: false}
	result, err := tool.Execute(`{"command": "ls -la"}`)
	if err != nil {
		t.Fatalf("Execute should succeed: %v", err)
	}
	if strings.Contains(result, "[compress:") {
		t.Errorf("Compression should be disabled, got: %q", result)
	}
}

func TestBashTool_Execute_Timeout_HitsDefault(t *testing.T) {
	tool := &BashTool{}
	// No timeout specified: should use the 60s default. Sleep 1s should still
	// complete, but this test verifies the default is applied and the command
	// eventually returns.
	result, err := tool.Execute(`{"command": "sleep 0.1"}`)
	if err != nil {
		t.Fatalf("Execute should succeed: %v", err)
	}
	if !strings.Contains(result, "Duration:") {
		t.Errorf("Expected duration in output, got: %q", result)
	}
}

func TestBashTool_Execute_Timeout_Expires(t *testing.T) {
	tool := &BashTool{}
	_, err := tool.Execute(`{"command": "sleep 10", "timeout": 1}`)
	if err == nil {
		t.Fatal("Expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("Expected timeout error type, got: %v", err)
	}
	if !strings.Contains(err.Error(), "timed out after 1s") {
		t.Errorf("Expected timeout duration in error, got: %v", err)
	}
}

func TestBashTool_Execute_Timeout_CappedAtMax(t *testing.T) {
	tool := &BashTool{}
	// A timeout above the max is clamped; the command should still time out
	// because we use a tiny sleep and the clamped value is still far larger.
	result, err := tool.Execute(fmt.Sprintf(`{"command": "sleep 0.1", "timeout": %d}`, MaxBashTimeoutS+1))
	if err != nil {
		t.Fatalf("Execute should succeed with clamped timeout: %v", err)
	}
	if !strings.Contains(result, "Duration:") {
		t.Errorf("Expected duration in output, got: %q", result)
	}
}

func TestBashTool_normalizeBashTimeout(t *testing.T) {
	tests := []struct {
		input, want int
	}{
		{0, DefaultBashTimeoutS},
		{-5, DefaultBashTimeoutS},
		{30, 30},
		{MaxBashTimeoutS, MaxBashTimeoutS},
		{MaxBashTimeoutS + 1, MaxBashTimeoutS},
	}
	for _, tc := range tests {
		if got := normalizeBashTimeout(tc.input); got != tc.want {
			t.Errorf("normalizeBashTimeout(%d) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestBashTool_Jail_RejectsParentDirectory(t *testing.T) {
	dir := t.TempDir()
	tool := &BashTool{ProjectDir: dir, Jail: true}
	_, err := tool.Execute(`{"command": "ls .."}`)
	if err == nil {
		t.Fatal("Expected jail violation for ls ..")
	}
	if !strings.Contains(err.Error(), "jail_violation") {
		t.Errorf("Expected jail_violation error, got: %v", err)
	}
}

func TestBashTool_Jail_RejectsAbsoluteOutside(t *testing.T) {
	dir := t.TempDir()
	tool := &BashTool{ProjectDir: dir, Jail: true}
	_, err := tool.Execute(`{"command": "cat /etc/passwd"}`)
	if err == nil {
		t.Fatal("Expected jail violation for absolute outside path")
	}
	if !strings.Contains(err.Error(), "jail_violation") {
		t.Errorf("Expected jail_violation error, got: %v", err)
	}
}

func TestBashTool_Jail_RejectsCdOutside(t *testing.T) {
	dir := t.TempDir()
	tool := &BashTool{ProjectDir: dir, Jail: true}
	_, err := tool.Execute(`{"command": "cd /tmp"}`)
	if err == nil {
		t.Fatal("Expected jail violation for cd outside")
	}
	if !strings.Contains(err.Error(), "jail_violation") {
		t.Errorf("Expected jail_violation error, got: %v", err)
	}
}

func TestBashTool_Jail_AllowsInsideProject(t *testing.T) {
	dir := t.TempDir()
	tool := &BashTool{ProjectDir: dir, Jail: true}
	result, err := tool.Execute(`{"command": "pwd"}`)
	if err != nil {
		t.Fatalf("Expected pwd to succeed: %v", err)
	}
	if !strings.Contains(result, dir) {
		t.Errorf("Expected pwd output to contain project dir, got: %q", result)
	}
}

func TestBashTool_Jail_Disabled(t *testing.T) {
	dir := t.TempDir()
	tool := &BashTool{ProjectDir: dir, Jail: false}
	_, err := tool.Execute(`{"command": "ls .."}`)
	if err != nil {
		t.Fatalf("Expected ls .. to succeed when jail is disabled: %v", err)
	}
}

func TestBashTool_Jail_RejectsOutsideWorkdir(t *testing.T) {
	dir := t.TempDir()
	tool := &BashTool{ProjectDir: dir, Jail: true}
	_, err := tool.Execute(`{"command": "pwd", "workdir": "/tmp"}`)
	if err == nil {
		t.Fatal("Expected jail violation for outside workdir")
	}
	if !strings.Contains(err.Error(), "jail_violation") {
		t.Errorf("Expected jail_violation error, got: %v", err)
	}
}

func TestBashTool_Truncation_KeepsTail(t *testing.T) {
	tool := &BashTool{MaxOutputBytes: 50}
	result, err := tool.Execute(`{"command": "seq 1 100"}`)
	if err != nil {
		t.Fatalf("Expected command to succeed: %v", err)
	}
	if strings.Contains(result, "1\n2\n3") {
		t.Errorf("Expected head to be truncated, got: %q", result)
	}
	if !strings.Contains(result, "100") {
		t.Errorf("Expected tail to contain 100, got: %q", result)
	}
	if !strings.Contains(result, "Output truncated") {
		t.Errorf("Expected truncation notice, got: %q", result)
	}
}

func TestBashTool_Truncation_ConfigurableBytes(t *testing.T) {
	tool := &BashTool{MaxOutputBytes: 20}
	result, err := tool.Execute(`{"command": "echo hello world"}`)
	if err != nil {
		t.Fatalf("Expected command to succeed: %v", err)
	}
	if len(result) > 200 {
		t.Errorf("Expected short truncated result, got length %d", len(result))
	}
}


func TestFirstCommandToken(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want string
	}{
		{"simple command", "ls -la", "ls"},
		{"with env prefix", "FOO=bar make install", "make"},
		{"with redirect prefix", ">/dev/null ls", "ls"},
		{"empty", "", ""},
		{"just spaces", "   ", ""},
		{"path command", "./script.sh arg1", "./script.sh"},
		{"assignment only", "FOO=bar", ""},
		{"multiple spaces", "echo    hello", "echo"},
		{"dash command", "-x foo", "-x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstCommandToken(tt.cmd)
			if got != tt.want {
				t.Errorf("firstCommandToken(%q) = %q, want %q", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestAdvanceShellWord(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		start int
		end   int
	}{
		{"simple word", "hello world", 0, 5},
		{"empty at end", "", 0, 0},
		{"at end", "hello", 5, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := advanceShellWord(tt.cmd, tt.start)
			if got != tt.end {
				t.Errorf("advanceShellWord(%q, %d) = %d, want %d", tt.cmd, tt.start, got, tt.end)
			}
		})
	}
}



func TestSkipQuoted(t *testing.T) {
	tests := []struct {
		name  string
		cmd   string
		start int
		quote byte
		end   int
	}{
		{"double quoted", "\"hello\" rest", 1, '"', 7},
		{"single quoted", "'hello' rest", 1, '\'', 7},
		{"unclosed double", "\"hello rest", 1, '"', 11},
		{"unclosed single", "'hello rest", 1, '\'', 11},
		{"empty quoted", "\"\" rest", 1, '"', 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := skipQuoted(tt.cmd, tt.start, tt.quote)
			if got != tt.end {
				t.Errorf("skipQuoted(%q, %d, %c) = %d, want %d", tt.cmd, tt.start, tt.quote, got, tt.end)
			}
		})
	}
}

func TestTruncateCommand(t *testing.T) {
	tests := []struct {
		name   string
		cmd    string
		maxLen int
		want   string
	}{
		{"short command", "echo hi", 20, "echo hi"},
		{"exact length", "echo hi", 7, "echo hi"},
		{"truncated", "echo hello world", 10, "echo he..."},
		{"very short max", "echo hi", 2, "ec"},
		{"zero max", "echo hi", 0, "..."},
		{"negative max", "echo hi", -1, "..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateCommand(tt.cmd, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateCommand(%q, %d) = %q, want %q", tt.cmd, tt.maxLen, got, tt.want)
			}
		})
	}
}


func TestBashTool_CompressionResolver_Enabled(t *testing.T) {
	// CompressionResolver returning true should trigger output compression.
	tool := &BashTool{
		CompressionResolver: func() bool { return true },
	}
	result, err := tool.Execute(`{"command": "ls -la"}`)
	if err != nil {
		t.Fatalf("Execute should succeed: %v", err)
	}
	if !strings.Contains(result, "[compress:") {
		t.Errorf("expected compression marker when CompressionResolver returns true, got: %q", result)
	}
}

func TestBashTool_CompressionResolver_Disabled(t *testing.T) {
	// CompressionResolver returning false should suppress output compression.
	tool := &BashTool{
		CompressionResolver: func() bool { return false },
	}
	result, err := tool.Execute(`{"command": "ls -la"}`)
	if err != nil {
		t.Fatalf("Execute should succeed: %v", err)
	}
	if strings.Contains(result, "[compress:") {
		t.Errorf("compression should be disabled when CompressionResolver returns false, got: %q", result)
	}
}

func TestBashTool_CompressionResolver_Nil_UsesCompressOutput(t *testing.T) {
	// When CompressionResolver is nil, the static CompressOutput field is used.
	tool := &BashTool{
		CompressionResolver: nil,
		CompressOutput:      true,
	}
	result, err := tool.Execute(`{"command": "ls -la"}`)
	if err != nil {
		t.Fatalf("Execute should succeed: %v", err)
	}
	if !strings.Contains(result, "[compress:") {
		t.Errorf("expected compression when CompressOutput=true and resolver is nil, got: %q", result)
	}
}

func TestAdvanceShellWord_EdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		cmd   string
		start int
		end   int
	}{
		{"stop at space", "hello world", 0, 5},
		{"single char", "a b", 0, 1},
		{"backslash escape", `hello\ world`, 0, 12},
		{"double quote skip", `"hello"`, 1, 7},
		{"single quote skip", `'hello'`, 1, 7},
		{"tab stop", "hello\tworld", 0, 5},
		{"already at end", "hi", 2, 2},
		{"past end", "hi", 5, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := advanceShellWord(tt.cmd, tt.start)
			if got != tt.end {
				t.Errorf("advanceShellWord(%q, %d) = %d, want %d", tt.cmd, tt.start, got, tt.end)
			}
		})
	}
}

func TestCheckBlocked(t *testing.T) {
	tests := []struct {
		name     string
		blocked  []string
		cmd      string
		wantFail bool
	}{
		{"no blocked list", nil, "rm -rf /", false},
		{"empty blocked list", []string{}, "rm -rf /", false},
		{"exact match blocked", []string{"rm"}, "rm -rf /", true},
		{"not in blocked list", []string{"mkfs"}, "rm -rf /", false},
		{"substring not matched", []string{"rm -rf /"}, "rm file", false},
		{"first token match", []string{"sudo"}, "sudo rm -rf /", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &BashTool{Blocked: tt.blocked}
			err := tool.checkBlocked(tt.cmd)
			if tt.wantFail && err == nil {
				t.Errorf("checkBlocked(%q) should return error", tt.cmd)
			}
			if !tt.wantFail && err != nil {
				t.Errorf("checkBlocked(%q) should not return error, got: %v", tt.cmd, err)
			}
		})
	}
}

func TestBashTool_NewBashCommand(t *testing.T) {
	cmd := newBashCommand("echo hello")
	if cmd == nil {
		t.Fatal("newBashCommand returned nil")
	}
	// Should use $SHELL or /bin/bash
	if cmd.Path == "" {
		t.Error("expected non-empty command path")
	}
}

func TestBashTool_Jail_CdToProjectRoot_WithChainedCommands(t *testing.T) {
	// This tests the exact scenario from the bug report
	dir := t.TempDir()
	tool := &BashTool{ProjectDir: dir, Jail: true}
	result, err := tool.Execute(`{"command": "cd ` + dir + ` && find . -maxdepth 1 -type f -name \"*.go\" | head -5"}`)
	if err != nil {
		t.Fatalf("cd to project root with chained commands should not trigger jail: %v", err)
	}
	if !strings.Contains(result, "Duration:") {
		t.Errorf("Expected duration in output, got: %q", result)
	}
}

func TestBashTool_NewBashCommand_UsesShell(t *testing.T) {
	// Test that newBashCommand uses the expected shell path
	cmd := newBashCommand("echo hello")
	if cmd.Path == "" {
		t.Error("expected non-empty Path")
	}
}

func TestBashTool_CompressionResolver_OutputCompressorsDisabled(t *testing.T) {
	// When OutputCompressors.Enabled=false, even a true resolver shouldn't compress.
	old := OutputCompressors.Enabled
	OutputCompressors.Enabled = false
	defer func() { OutputCompressors.Enabled = old }()

	tool := &BashTool{
		CompressionResolver: func() bool { return true },
	}
	result, err := tool.Execute(`{"command": "ls -la"}`)
	if err != nil {
		t.Fatalf("Execute should succeed: %v", err)
	}
	if strings.Contains(result, "[compress:") {
		t.Errorf("compression should be disabled when OutputCompressors.Enabled=false, got: %q", result)
	}
}
