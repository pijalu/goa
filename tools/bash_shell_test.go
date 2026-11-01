package tools

import (
	"context"
	"strings"
	"testing"
)

// B48: the bash tool must always run bash unless the user explicitly opts
// into the login shell (tools.bash.shell: user). A user account whose $SHELL
// points at fish/zsh/pwsh must not silently change how goa's own commands
// are interpreted — fish is not POSIX, and non-bash shells break plain
// commands the model emits.

// TestBashTool_DefaultShellIgnoresUserShell pins the B48 default: the bash
// tool executes via bash, never via whatever $SHELL happens to be.
func TestBashTool_DefaultShellIgnoresUserShell(t *testing.T) {
	t.Setenv("SHELL", "/definitely/not/a/real/shell")
	tool := &BashTool{}
	out, err := tool.ExecuteContext(context.Background(), `{"command":"echo b48-ok"}`)
	if err != nil {
		t.Fatalf("default BashTool must execute via bash even when $SHELL is unusable: %v\nout=%q", err, out)
	}
	if !strings.Contains(out, "b48-ok") {
		t.Fatalf("command output missing marker: %q", out)
	}
}

// TestBashTool_UserShellModeHonorsSHELL pins the opt-in: with Shell="user"
// the tool really runs the login shell — a broken $SHELL must surface as a
// command failure instead of being silently swapped for bash.
func TestBashTool_UserShellModeHonorsSHELL(t *testing.T) {
	t.Setenv("SHELL", "/definitely/not/a/real/shell")
	tool := &BashTool{Shell: "user"}
	if _, err := tool.ExecuteContext(context.Background(), `{"command":"echo b48-ok"}`); err == nil {
		t.Fatal("Shell=\"user\" must honor $SHELL (a broken $SHELL must fail the command), got success")
	}
}

// TestResolveShell_DefaultIsBash covers the fail-safe mapping: everything
// except the explicit "user" opt-in resolves to bash.
func TestResolveShell_DefaultIsBash(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	for _, pref := range []string{"", "bash", "zsh", "/bin/fish", "USER"} {
		if got := resolveShell(pref); got != defaultBashShell {
			t.Fatalf("resolveShell(%q) = %q, want %q (unknown values must fail safe to bash)", pref, got, defaultBashShell)
		}
	}
}

func TestResolveShell_UserHonorsSHELL(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	if got := resolveShell("user"); got != "/bin/zsh" {
		t.Fatalf("resolveShell(\"user\") = %q, want /bin/zsh", got)
	}
}

func TestResolveShell_UserFallsBackToBash(t *testing.T) {
	t.Setenv("SHELL", "")
	if got := resolveShell("user"); got != defaultBashShell {
		t.Fatalf("resolveShell(\"user\") with empty $SHELL = %q, want %q", got, defaultBashShell)
	}
}

// TestBashTool_DescriptionNamesShellInUserMode: in user-shell mode the tool
// description must name the resolved shell so the LLM emits syntax the
// actual shell understands (B48).
func TestBashTool_DescriptionNamesShellInUserMode(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	desc := (&BashTool{Shell: "user"}).Schema().Description
	if !strings.Contains(desc, "/bin/zsh") {
		t.Fatalf("user-mode description must name the resolved shell, got: %q", desc)
	}
}

// The default mode stays quiet: bash is the assumption, no shell suffix.
func TestBashTool_DefaultDescriptionOmitsShellName(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	desc := (&BashTool{}).Schema().Description
	if strings.Contains(desc, "/bin/zsh") {
		t.Fatalf("default description must not advertise the login shell, got: %q", desc)
	}
}
