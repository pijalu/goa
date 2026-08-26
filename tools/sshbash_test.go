package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSHBashTool_ExecuteContext_CancelledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tool := &SSHBashTool{Hosts: []SSHHostConfig{{ID: "test", Host: "example.org"}}}
	_, err := tool.ExecuteContext(ctx, `{"host_id":"test","command":"sleep 10"}`)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected cancellation error, got %v", err)
	}
}

func TestSSHBashTool_ExecuteContext_TimeoutKillsCommand(t *testing.T) {
	dir := t.TempDir()
	sshPath := filepath.Join(dir, "ssh")
	if err := os.WriteFile(sshPath, []byte("#!/bin/sh\nsleep 10\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)

	tool := &SSHBashTool{Hosts: []SSHHostConfig{{ID: "test", Host: "example.org"}}}
	_, err := tool.ExecuteContext(context.Background(), `{"host_id":"test","command":"sleep 10","timeout":1}`)
	if err == nil || !strings.Contains(err.Error(), "timed out after 1s") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}
