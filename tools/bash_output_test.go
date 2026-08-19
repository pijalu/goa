package tools

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBashTool_Execute_SanitizesControlBytes(t *testing.T) {
	tool := &BashTool{}
	out, err := tool.Execute(`{"command":"printf '\\033[2Kdone\\033[0m\\n'"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("raw ESC byte leaked into tool output: %q", out)
	}
	if !strings.Contains(out, `\e[2Kdone`) {
		t.Errorf("expected escape sequence shown as literal text, got: %q", out)
	}
}

func TestTruncateCommand_RuneSafe(t *testing.T) {
	cmd := strings.Repeat("世", 10)
	got := truncateCommand(cmd, 5)
	if !utf8.ValidString(got) {
		t.Errorf("truncateCommand split a rune: %q", got)
	}
}

func TestBashTool_Redactor_Nil_DoesNotChange(t *testing.T) {
	tool := &BashTool{}
	result, err := tool.Execute(`{"command": "echo hello"}`)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(result, "hello") {
		t.Errorf("expected output unchanged, got: %q", result)
	}
}
